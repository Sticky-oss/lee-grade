// model.go defines the bubbletea Model + its Init / Update entry points.
//
// Model owns:
//   - the loaded Task (immutable for the duration of the session)
//   - the current TaskResult (updates whenever a grade finishes)
//   - the embedded PTY bash subshell + its accumulated output buffer
//   - terminal dimensions
//
// The Update method is the event loop: every tea.Msg flows through here.
// Key bindings:
//   - Ctrl+Q          quit
//   - Ctrl+G          manual re-grade (Enter also auto-grades after a beat)
//   - Ctrl+R          clear the right-pane scrollback (not a sim reset —
//                     the host state is unchanged)
//   - F1              toggle the inline help footer
//   - any other key   → forwarded to the PTY's stdin
package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sticky-oss/lee-grade/internal/check"
	"github.com/sticky-oss/lee-grade/internal/task"
)

// Maximum scrollback retained for the right pane. PTY output beyond
// this gets dropped from the head. 8 KiB ≈ ~100 typical-density terminal
// lines — enough to see the recent command + its output, not so much
// that lipgloss repaint cost dominates frame time on big outputs.
const ptyScrollbackBytes = 8 * 1024

// Model is the bubbletea Model.
type Model struct {
	t           *task.Task
	result      *check.TaskResult
	pty         *PtySession
	ptyBuf      []byte // accumulated PTY output (truncated to ptyScrollbackBytes)
	width       int
	height      int
	showHelp    bool
	quitting    bool
	passthrough bool // true while vim/nano/etc owns the terminal
}

// NewModel constructs a Model around a loaded Task. The PTY is spawned
// here so a failure to start the subshell is reported as a setup error
// (and not silently inside the TUI).
func NewModel(t *task.Task) (*Model, error) {
	if err := t.Validate(); err != nil {
		return nil, err
	}
	pty, err := NewPty()
	if err != nil {
		return nil, err
	}
	return &Model{t: t, pty: pty}, nil
}

// LastResult exposes the most recent grading result so main() can print
// a summary line after the TUI exits. Returns nil if no grade has run.
func (m *Model) LastResult() *check.TaskResult { return m.result }

// Init runs the initial Cmd batch: kick off the PTY read pump and run
// the first grade immediately so the learner sees the starting ✓/✗
// state before they type anything.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(
		m.pty.ReadLoop(),
		gradeCmd(m.t),
	)
}

// Update is the bubbletea event dispatcher.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = v.Width
		m.height = v.Height
		// The right pane is roughly 60% of the width minus borders;
		// resize the PTY so bash's line-wrap matches what the user sees.
		cols, rows := m.shellPaneDims()
		m.pty.Resize(uint16(cols), uint16(rows))
		return m, nil

	case ptyOutputMsg:
		m.appendPty([]byte(v))
		// Re-issue the read pump so we keep getting subsequent chunks.
		return m, m.pty.ReadLoop()

	case passthroughEnteredMsg:
		// Any pre-sequence bytes flow to the normal pane FIRST so the
		// prompt that preceded the vim launch is preserved when we
		// resume. Then we hand the screen to the PTY: ExitAltScreen
		// drops bubbletea's renderer + alt-screen, and runPassthrough
		// takes over with raw-mode stdin ↔ PTY ↔ raw stdout.
		m.appendPty([]byte(v.bytesBeforeSeq))
		m.passthrough = true
		return m, tea.Sequence(
			tea.ExitAltScreen,
			runPassthrough(m.pty, m.pty.Scan, m.pty.altInitial),
		)

	case passthroughDoneMsg:
		// Vim / nano / etc exited. We already emitted the alt-screen-
		// exit sequence (the PTY did, and we forwarded). Now take
		// back the terminal: re-enter alt-screen, resume the
		// renderer, re-issue the PTY read pump for normal bytes,
		// and force a re-grade because the user may have just saved
		// a config file that flips a check.
		m.passthrough = false
		return m, tea.Sequence(
			tea.EnterAltScreen,
			m.pty.ReadLoop(),
			gradeCmd(m.t),
		)

	case ptyClosedMsg:
		// Subshell exited — fold the TUI cleanly. The user typically
		// triggered this with `exit` or Ctrl+D.
		m.quitting = true
		return m, tea.Quit

	case gradeResultMsg:
		m.result = v.result
		return m, nil

	case tea.KeyMsg:
		switch v.String() {
		case "ctrl+q":
			m.quitting = true
			m.pty.Close()
			return m, tea.Quit
		case "ctrl+g":
			return m, gradeCmd(m.t)
		case "ctrl+r":
			// Clear the right pane's scrollback (host state unchanged).
			// Send a literal `clear` to bash too so its own state matches.
			m.ptyBuf = m.ptyBuf[:0]
			m.pty.Write([]byte("clear\r"))
			return m, nil
		case "f1":
			m.showHelp = !m.showHelp
			return m, nil
		case "enter":
			// Send the keystroke to the shell, AND schedule a re-grade
			// (a guess that an Enter usually finishes a command worth
			// re-grading after).
			m.pty.Write([]byte("\r"))
			return m, gradeCmd(m.t)
		default:
			// Forward every other keystroke to the PTY. bubbletea's
			// KeyMsg.String() loses the raw bytes for some keys, so
			// reach into Runes / Type to reconstruct.
			m.pty.Write(keyBytes(v))
			return m, nil
		}
	}
	return m, nil
}

// appendPty grows the scrollback buffer and trims the head when over
// the cap. Trimming to a NL boundary keeps line wraps clean.
func (m *Model) appendPty(b []byte) {
	m.ptyBuf = append(m.ptyBuf, b...)
	if len(m.ptyBuf) <= ptyScrollbackBytes {
		return
	}
	drop := len(m.ptyBuf) - ptyScrollbackBytes
	// Round drop up to the next NL so we don't slice a line mid-byte.
	nl := strings.IndexByte(string(m.ptyBuf[drop:]), '\n')
	if nl >= 0 {
		drop += nl + 1
	}
	if drop > len(m.ptyBuf) {
		drop = len(m.ptyBuf)
	}
	m.ptyBuf = append(m.ptyBuf[:0], m.ptyBuf[drop:]...)
}

// shellPaneDims returns the (cols, rows) the PTY should report to the
// shell so bash's line-wrap matches what we render. Must stay in sync
// with the width math in view.go's renderBodyThreePane /
// renderBodyTwoPane (~44% on wide layouts, ~60% on narrow).
func (m *Model) shellPaneDims() (cols, rows int) {
	if m.width == 0 {
		return 80, 24
	}
	if m.width < narrowLayoutWidth {
		// Narrow: terminal takes the right ~60% of the screen.
		cols = (m.width * 6 / 10) - 2
	} else {
		// Wide three-pane: terminal is the middle ~44% (100 - 28*2).
		cols = (m.width * 44 / 100) - 2
	}
	if cols < 20 {
		cols = 20
	}
	// 4 rows reserved for the header + footer + status. Render area
	// is the rest.
	rows = m.height - 4
	if rows < 5 {
		rows = 5
	}
	return cols, rows
}

// keyBytes turns a bubbletea KeyMsg back into the bytes a real terminal
// would have written. bubbletea pre-parses common escape sequences (Ctrl,
// arrows, etc.) into typed Key values; for forwarding to a PTY we need
// to undo that and write the original byte sequence.
func keyBytes(k tea.KeyMsg) []byte {
	if len(k.Runes) > 0 {
		return []byte(string(k.Runes))
	}
	switch k.Type {
	case tea.KeyEnter:
		return []byte{'\r'}
	case tea.KeyBackspace:
		return []byte{0x7f}
	case tea.KeyTab:
		return []byte{'\t'}
	case tea.KeyEscape:
		return []byte{0x1b}
	case tea.KeySpace:
		return []byte{' '}
	case tea.KeyUp:
		return []byte("\x1b[A")
	case tea.KeyDown:
		return []byte("\x1b[B")
	case tea.KeyRight:
		return []byte("\x1b[C")
	case tea.KeyLeft:
		return []byte("\x1b[D")
	case tea.KeyHome:
		return []byte("\x1b[H")
	case tea.KeyEnd:
		return []byte("\x1b[F")
	case tea.KeyDelete:
		return []byte("\x1b[3~")
	case tea.KeyPgUp:
		return []byte("\x1b[5~")
	case tea.KeyPgDown:
		return []byte("\x1b[6~")
	}
	// Ctrl+<letter> chords. bubbletea exposes these as KeyCtrl* enums.
	if k.Type >= tea.KeyCtrlA && k.Type <= tea.KeyCtrlZ {
		// Ctrl+A is 0x01, Ctrl+B is 0x02, etc.
		return []byte{byte(k.Type - tea.KeyCtrlA + 1)}
	}
	return nil
}
