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
type PtySession struct {
	cmd  *exec.Cmd
	ptmx *os.File // master side of the PTY pair
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
	return &PtySession{cmd: c, ptmx: ptmx}, nil
}

// ReadLoop returns a tea.Cmd that reads ONE chunk from the PTY and emits
// it as a ptyOutputMsg. The caller (Update) re-issues the Cmd on every
// receipt — that's how bubbletea models long-running event sources.
//
// 4 KiB chunks are large enough for normal command output (ls, cat) and
// small enough to keep the Update loop responsive on bursty output (a
// `cat /var/log/messages` doesn't freeze the UI).
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
		return ptyOutputMsg(buf[:n])
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
