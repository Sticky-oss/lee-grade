// Package tui hosts the bubbletea TUI that wraps lee-grade's check
// engine in a two-pane lab experience.
//
// pty.go owns the embedded bash subshell: spawn it on a real PTY, route
// keystrokes from the bubbletea event loop into its stdin, and pump its
// output back as tea.Msg events so the right-side viewport can render
// it. The subshell is a child process — when the TUI exits, it gets a
// SIGHUP via PTY close.
package tui

import (
	"errors"
	"io"
	"os"
	"os/exec"

	"github.com/creack/pty"
	tea "github.com/charmbracelet/bubbletea"
)

// ptyOutputMsg carries a chunk of bytes from the embedded shell's stdout
// into the bubbletea Update loop. We deliberately pass raw bytes (not a
// rendered string) so escape sequences for color codes survive intact —
// the right-pane renderer hands them to lipgloss which respects ANSI.
type ptyOutputMsg []byte

// ptyClosedMsg signals the subshell exited (or the PTY was closed). The
// TUI treats this as terminal-style "the shell died" — the user usually
// triggers it via `exit` or Ctrl+D in the right pane. We map it onto a
// graceful TUI shutdown.
type ptyClosedMsg struct{ err error }

// PtySession wraps the spawned bash subshell + its master-side PTY fd.
// Methods are safe to call from the bubbletea Update goroutine; the
// read pump runs in a dedicated goroutine and sends ptyOutputMsg via
// tea.Program.Send.
//
// The Scanner field tracks whether the subprocess inside the PTY is
// currently in alt-screen mode (vim / nano / less / htop / …). When it
// flips true, Update releases the terminal and hands it directly to
// the PTY for the duration; when it flips false, bubbletea takes it
// back. See altscreen.go for the full design.
type PtySession struct {
	cmd  *exec.Cmd
	ptmx *os.File // master side of the PTY pair
	Scan *Scanner
	// altInitial buffers bytes that arrived in the same read AS the
	// alt-screen-enter sequence but came AFTER it. The passthrough
	// goroutine grabs them on startup so vim's opening paint isn't
	// truncated. Cleared after each handoff.
	altInitial []byte
}

// NewPty spawns `bash -i` on a PTY. Interactive mode is what produces
// the normal `$ ` / `# ` prompt, job control, etc. — without `-i` the
// shell runs in a quieter mode that confuses learners ("where's my
// prompt?").
//
// Initial size is 24×80 (classic VT default); the caller resizes via
// Resize() once bubbletea reports the actual pane dimensions in the
// first WindowSizeMsg.
func NewPty() (*PtySession, error) {
	c := exec.Command("/bin/bash", "-i")
	// Inherit the parent env so PATH / HOME / etc. work the way the
	// user expects. SHELL is set explicitly so child processes that
	// re-exec their shell pick bash, not whatever was inherited.
	c.Env = append(os.Environ(), "SHELL=/bin/bash", "TERM=xterm-256color")
	ptmx, err := pty.Start(c)
	if err != nil {
		return nil, err
	}
	return &PtySession{cmd: c, ptmx: ptmx, Scan: NewScanner()}, nil
}

// ReadLoop returns a tea.Cmd that reads ONE chunk from the PTY and
// emits either:
//   - ptyOutputMsg for normal text (drained to the right-pane viewport)
//   - passthroughEnteredMsg when an alt-screen-enter sequence is
//     detected, with any pre-sequence bytes still drained normally
//
// The caller (Update) re-issues ReadLoop on every receipt of
// ptyOutputMsg; on passthroughEnteredMsg it switches to runPassthrough
// instead, and re-issues ReadLoop only after passthroughDoneMsg.
//
// 4 KiB chunks are large enough for normal command output and small
// enough to keep the Update loop responsive on bursty output.
func (p *PtySession) ReadLoop() tea.Cmd {
	return func() tea.Msg {
		buf := make([]byte, 4096)
		n, err := p.ptmx.Read(buf)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return ptyClosedMsg{nil}
			}
			return ptyClosedMsg{err}
		}
		// Scan for alt-screen-enter. On hit, the bytes BEFORE the
		// sequence flow to the normal pane (via the msg's
		// bytesBeforeSeq); the bytes AFTER are buffered inside the
		// Scanner and the passthrough goroutine picks them up via
		// its initialBytes path.
		before, found, after := p.Scan.ScanEnter(buf[:n])
		if found {
			// Stash the after-bytes on the session so Update can pass
			// them into runPassthrough. We use a small dedicated field
			// (not the scanner's tail) so the carry-bytes-across-reads
			// logic stays independent of the enter/exit hand-off.
			p.altInitial = append(p.altInitial[:0], after...)
			return passthroughEnteredMsg{bytesBeforeSeq: before}
		}
		return ptyOutputMsg(before)
	}
}

// Write forwards bytes from the bubbletea keyboard event into the PTY's
// stdin. Errors are swallowed — a write failure usually means the
// subshell died, which we'll discover next time ReadLoop fires.
func (p *PtySession) Write(b []byte) {
	if p.ptmx == nil {
		return
	}
	_, _ = p.ptmx.Write(b)
}

// Resize tells the kernel the PTY is now WxH cells. Programs running on
// the slave side (bash, vim, less) re-flow their output on receipt.
// Called from the WindowSizeMsg handler whenever the right pane changes
// size.
func (p *PtySession) Resize(cols, rows uint16) {
	if p.ptmx == nil {
		return
	}
	_ = pty.Setsize(p.ptmx, &pty.Winsize{Rows: rows, Cols: cols})
}

// Close kills the subshell and frees the PTY pair. Safe to call multiple
// times; second-and-after calls are no-ops.
func (p *PtySession) Close() {
	if p.ptmx != nil {
		_ = p.ptmx.Close()
		p.ptmx = nil
	}
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
}
