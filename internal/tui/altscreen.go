// altscreen.go owns the detection + passthrough plumbing for full-screen
// TUI subprograms (vi, nano, less, htop, …) running inside the embedded
// PTY.
//
// Why this exists
//
// Bubbletea owns the terminal: it sits in its own alternate-screen
// buffer, repaints on every frame, manages cursor position. When the
// PTY subshell runs vim, vim ALSO wants to own the terminal — it
// emits `\x1b[?1049h` to enter alt-screen, then drives cursor / scroll
// / repaint sequences for the duration of the edit. If we just pipe
// vim's bytes into the right-pane viewport, bubbletea's next repaint
// over-writes vim's painting; vim's next cursor-position sequence
// moves the cursor inside our pane instead of where vim wanted it.
// Net result: glitchy, unreadable.
//
// What we do
//
// Watch the PTY's output stream byte-by-byte for the canonical alt-
// screen enter sequences (1049, 1047, 47 — modern, intermediate, and
// xterm-original). When detected:
//   1. Drain the bytes BEFORE the sequence into the normal viewport.
//   2. Ask bubbletea to release the terminal (exit alt-screen, exit
//      raw mode, kill the renderer — all the way back to a clean tty).
//   3. Put the user's tty into raw mode (vim expects raw).
//   4. Run a passthrough loop: copy os.Stdin → PTY, copy PTY → os.Stdout,
//      bytes flow unmediated. While this is running, vim has the screen
//      to itself; the user types, vim renders, nobody clobbers anybody.
//   5. Continue scanning the PTY → os.Stdout stream for the matching
//      exit sequence (1049l / 1047l / 47l). When seen, write the exit
//      sequence through (so the terminal restores the original screen
//      buffer), restore the tty's cooked state, and send a
//      passthroughDoneMsg back to bubbletea.
//   6. Bubbletea handles the msg by re-entering its own alt-screen,
//      raw mode, renderer. Three-pane TUI resumes; PTY read loop
//      resumes; nothing was lost.
//
// Cross-read split protection
//
// A naive `bytes.Index(buf, seq)` scan misses sequences that arrive
// split across two read() calls. The Scanner type below carries a
// 7-byte tail buffer between reads so a sequence boundary at byte N of
// one read + bytes 0..M of the next still matches. 7 bytes is enough
// for the longest sequence we watch (`\x1b[?1049h` = 8 bytes; we keep
// 7 of trailing context so the head can append + match).
package tui

import (
	"bytes"
	"io"
	"os"
	"sync/atomic"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"
)

// Recognised alt-screen sequences. The h/l (high/low) suffix is the
// canonical "set mode" / "reset mode" ANSI convention.
var (
	altEnterSeqs = [][]byte{
		[]byte("\x1b[?1049h"), // modern xterm (vim, less, htop default)
		[]byte("\x1b[?1047h"), // intermediate
		[]byte("\x1b[?47h"),   // oldest
	}
	altExitSeqs = [][]byte{
		[]byte("\x1b[?1049l"),
		[]byte("\x1b[?1047l"),
		[]byte("\x1b[?47l"),
	}
)

// passthroughEnteredMsg signals the bubbletea Update loop that the PTY
// emitted an alt-screen-enter sequence and we're about to take over
// the terminal directly. Update handles it by calling
// p.ReleaseTerminal() and kicking off the passthrough goroutine.
type passthroughEnteredMsg struct {
	// bytesBeforeSeq is the chunk of PTY output that arrived in the
	// same read AS the alt-screen-enter sequence but came BEFORE it.
	// Update appends these to the normal viewport before flipping to
	// passthrough mode.
	bytesBeforeSeq []byte
}

// passthroughDoneMsg signals the alt-screen subprogram exited and
// bubbletea should take back the terminal.
type passthroughDoneMsg struct{}

// Scanner is a stateful byte-scanner that tracks whether we've seen an
// alt-screen enter (and not yet a matching exit). It carries up to 7
// bytes of context across Scan() calls so a sequence that lands across
// a read boundary still matches.
//
// Single Scanner instance per PtySession.
type Scanner struct {
	tail        []byte // up to 7 bytes carried from prior Scan
	inAltScreen atomic.Bool
}

// NewScanner returns a fresh, ready-to-scan Scanner.
func NewScanner() *Scanner { return &Scanner{tail: make([]byte, 0, 8)} }

// InAltScreen is safe to read concurrently. The PTY-read goroutine and
// the passthrough goroutine both look at this flag.
func (s *Scanner) InAltScreen() bool { return s.inAltScreen.Load() }

// ScanEnter looks for an alt-screen-enter sequence in the freshly-read
// buffer `b`. On hit:
//   - returns (before, found=true): `before` is the bytes that came
//     BEFORE the sequence in this read (to be drained to the viewport)
//   - sets the inAltScreen flag
//   - any bytes AFTER the sequence are buffered for the passthrough
//     goroutine's first write (returned via `tail`)
//
// On miss: returns (b, false, nil); caller carries on with normal pane
// rendering.
//
// The tail-buffer carry handles split sequences: if the buffer ended
// with `\x1b[?104` and the NEXT read starts with `9h`, the tail is
// prepended on the next call so the full match is detected.
func (s *Scanner) ScanEnter(b []byte) (before []byte, found bool, afterTail []byte) {
	// IMPORTANT: build combined on its own backing so that the
	// returned `before` / `afterTail` slices don't alias s.tail's
	// memory. With shared backing, the subsequent s.tail update would
	// mutate the bytes `before` points at (caught by
	// TestScanner_DetectsEnterSequenceAcrossTwoReads).
	combined := make([]byte, 0, len(s.tail)+len(b))
	combined = append(combined, s.tail...)
	combined = append(combined, b...)
	for _, seq := range altEnterSeqs {
		if i := bytes.Index(combined, seq); i >= 0 {
			s.inAltScreen.Store(true)
			s.tail = s.tail[:0]
			return combined[:i], true, combined[i+len(seq):]
		}
	}
	// No match — keep up to the last 7 bytes as carry for next call.
	const carry = 7
	if len(combined) <= carry {
		s.tail = append(s.tail[:0], combined...)
		return combined, false, nil
	}
	// Drain everything except the last `carry` bytes into the caller;
	// keep the tail for the next scan.
	s.tail = append(s.tail[:0], combined[len(combined)-carry:]...)
	return combined[:len(combined)-carry], false, nil
}

// ScanExit looks for an alt-screen-exit sequence — same shape as
// ScanEnter but for the closing 1049l / 1047l / 47l.
func (s *Scanner) ScanExit(b []byte) (beforeAndIncludingSeq []byte, found bool, afterTail []byte) {
	combined := make([]byte, 0, len(s.tail)+len(b))
	combined = append(combined, s.tail...)
	combined = append(combined, b...)
	for _, seq := range altExitSeqs {
		if i := bytes.Index(combined, seq); i >= 0 {
			s.inAltScreen.Store(false)
			s.tail = s.tail[:0]
			// Include the exit sequence in the returned bytes so the
			// terminal sees it and restores the original screen buffer.
			end := i + len(seq)
			return combined[:end], true, combined[end:]
		}
	}
	const carry = 7
	if len(combined) <= carry {
		s.tail = append(s.tail[:0], combined...)
		return combined, false, nil
	}
	s.tail = append(s.tail[:0], combined[len(combined)-carry:]...)
	return combined[:len(combined)-carry], false, nil
}

// runPassthrough takes over the terminal for the duration of an
// alt-screen subprogram. It blocks until the PTY emits an alt-screen-
// exit sequence (or the PTY closes), then returns. The Cmd shape lets
// us return it from Update; bubbletea runs it on a separate goroutine.
//
// Inside, two copy goroutines move bytes:
//   - stdin → PTY  (forwarding raw key events to vim/nano/etc)
//   - PTY  → stdout (forwarding vim's screen paint to the user)
//
// The PTY → stdout copy is the one that scans for the exit sequence;
// when it fires, both copies wind down.
//
// `initialBytes` is the chunk that arrived AFTER the alt-screen-enter
// sequence in the same PTY read. We write it to stdout first thing so
// vim's opening paint is intact.
func runPassthrough(p *PtySession, sc *Scanner, initialBytes []byte) tea.Cmd {
	return func() tea.Msg {
		// Raw mode on the user's tty so keystrokes flow unbuffered.
		// Without this, bash's line discipline buffers Enter-terminated
		// lines and vim sees nothing until Enter — totally broken.
		state, err := term.MakeRaw(int(os.Stdin.Fd()))
		if err != nil {
			// Couldn't get raw mode — degrade gracefully: emit a warning
			// to bubbletea and stay in normal mode. The user will see
			// glitchy vim but at least the TUI doesn't deadlock.
			return passthroughDoneMsg{}
		}
		defer func() {
			_ = term.Restore(int(os.Stdin.Fd()), state)
		}()

		// Emit the alt-screen-enter sequence ourselves on the user's
		// tty so the terminal switches buffers BEFORE vim starts
		// painting. (Bubbletea's ReleaseTerminal exited its own alt-
		// screen; without our own enter, vim would paint over the
		// previous screen content.)
		_, _ = os.Stdout.Write([]byte("\x1b[?1049h"))
		if len(initialBytes) > 0 {
			_, _ = os.Stdout.Write(initialBytes)
		}

		done := make(chan struct{})

		// stdin → PTY pump.
		go func() {
			buf := make([]byte, 256)
			for {
				select {
				case <-done:
					return
				default:
				}
				n, err := os.Stdin.Read(buf)
				if n > 0 {
					p.Write(buf[:n])
				}
				if err != nil {
					return
				}
			}
		}()

		// PTY → stdout pump (the one that scans for exit).
		buf := make([]byte, 4096)
		for {
			n, err := p.ptmx.Read(buf)
			if n > 0 {
				out, found, _ := sc.ScanExit(buf[:n])
				_, _ = os.Stdout.Write(out)
				if found {
					close(done)
					return passthroughDoneMsg{}
				}
			}
			if err != nil {
				if err == io.EOF {
					return ptyClosedMsg{nil}
				}
				return ptyClosedMsg{err}
			}
		}
	}
}
