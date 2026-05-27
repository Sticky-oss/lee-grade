// Pure-logic unit tests for the bubbletea Model. Anything that requires
// a real TTY (the bubbletea event loop, PTY I/O, lipgloss rendering size
// effects) is exercised in the manual smoke-test path; here we cover
// the math + buffer truncation + KeyMsg → byte translation that has
// historically been the source of TUI bugs.
package tui

import (
	"bytes"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestKeyBytes_PrintableRunes(t *testing.T) {
	// Letters / digits round-trip through k.Runes.
	got := keyBytes(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("abc123")})
	if string(got) != "abc123" {
		t.Fatalf("printable runes: got %q, want %q", got, "abc123")
	}
}

func TestKeyBytes_NamedKeys(t *testing.T) {
	cases := []struct {
		name string
		k    tea.KeyType
		want []byte
	}{
		{"enter sends CR (carriage return)", tea.KeyEnter, []byte{'\r'}},
		{"backspace sends DEL (0x7f, what real terminals send)", tea.KeyBackspace, []byte{0x7f}},
		{"tab sends literal TAB", tea.KeyTab, []byte{'\t'}},
		{"escape sends literal ESC", tea.KeyEscape, []byte{0x1b}},
		{"up arrow sends CSI A", tea.KeyUp, []byte("\x1b[A")},
		{"down arrow sends CSI B", tea.KeyDown, []byte("\x1b[B")},
		{"right arrow sends CSI C", tea.KeyRight, []byte("\x1b[C")},
		{"left arrow sends CSI D", tea.KeyLeft, []byte("\x1b[D")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := keyBytes(tea.KeyMsg{Type: c.k})
			if !bytes.Equal(got, c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestKeyBytes_CtrlChord(t *testing.T) {
	// Ctrl+A is ASCII 0x01, Ctrl+C is 0x03, Ctrl+L is 0x0c.
	cases := []struct {
		name string
		k    tea.KeyType
		want byte
	}{
		{"Ctrl+A -> 0x01", tea.KeyCtrlA, 0x01},
		{"Ctrl+C -> 0x03 (SIGINT for the subshell)", tea.KeyCtrlC, 0x03},
		{"Ctrl+L -> 0x0c (bash clear-screen)", tea.KeyCtrlL, 0x0c},
		{"Ctrl+D -> 0x04 (EOF / exit)", tea.KeyCtrlD, 0x04},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := keyBytes(tea.KeyMsg{Type: c.k})
			if len(got) != 1 || got[0] != c.want {
				t.Fatalf("got %v, want byte %02x", got, c.want)
			}
		})
	}
}

func TestAppendPty_TruncatesToCapAtNewlineBoundary(t *testing.T) {
	m := &Model{}
	// Push 16 KiB of "x\n" lines — twice the scrollback cap.
	line := strings.Repeat("x", 79) + "\n"
	for i := 0; i < ptyScrollbackBytes/len(line)*2; i++ {
		m.appendPty([]byte(line))
	}
	if len(m.ptyBuf) > ptyScrollbackBytes {
		t.Fatalf("buffer not trimmed: len=%d cap=%d", len(m.ptyBuf), ptyScrollbackBytes)
	}
	// First byte after trim should be the start of a clean line, not a
	// mid-line slice. Lines all start with 'x', so this is a weak check;
	// stronger: no leading partial-byte garbage.
	if m.ptyBuf[0] != 'x' {
		t.Fatalf("buffer head not on line boundary: %q", m.ptyBuf[:10])
	}
}

func TestShellPaneDims_ClampsForSmallTerminals(t *testing.T) {
	// Wide path: terminal pane is the middle ~44% of the three-pane
	// layout. Narrow path: terminal pane is the right ~60% of the
	// two-pane fallback. Both floor at 20×5.
	cases := []struct {
		name              string
		w, h              int
		wantCols, wantRow int
	}{
		{"tiny terminal floors at 20×5 (narrow path)", 30, 8, 20, 5},
		{"99×30 — narrow path, ~60% width (=57 cols)", 99, 30, 57, 26},
		{"100×30 — wide path, ~44% width (=42 cols)", 100, 30, 42, 26},
		{"160×40 wide — 44% of 160 minus borders = 68 cols", 160, 40, 68, 36},
		{"zero size returns 80×24 default", 0, 0, 80, 24},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := &Model{width: c.w, height: c.h}
			cols, rows := m.shellPaneDims()
			if cols != c.wantCols || rows != c.wantRow {
				t.Fatalf("dims for %dx%d: got %dx%d, want %dx%d",
					c.w, c.h, cols, rows, c.wantCols, c.wantRow)
			}
		})
	}
}
