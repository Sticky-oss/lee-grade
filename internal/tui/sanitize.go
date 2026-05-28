// sanitize.go strips PTY control sequences that don't survive the lipgloss
// horizontal-join render path.
//
// The problem: bash output contains \r (carriage return) and a variety of
// CSI/OSC escape sequences for cursor positioning, line clearing, and
// window-title updates. When we render m.ptyBuf as the middle pane and
// JoinHorizontal stitches it next to the brief + progress panes on the
// same output rows, your real terminal interprets the embedded \r as
// "move cursor to column 0 of the current row" — column 0 of the joined
// row is INSIDE the left pane, so subsequent bytes overwrite the task
// description. \e[K (clear to end of line) and absolute cursor moves
// cause similar bleed-through. Net result: bash prompts in the TASK
// pane, garbled output everywhere.
//
// Solution: filter the bytes before they reach the renderer. Keep SGR
// (colour) sequences — those are display-attribute changes lipgloss
// understands and the terminal renders in place. Drop everything that
// could move the cursor or clear regions of the screen.
package tui

import "strings"

// sanitizePtyForViewport returns a render-safe string view of raw PTY
// bytes. Specifically:
//   - \r is dropped (\r\n collapses to \n; bare \r is also dropped — we
//     never want a carriage return inside a horizontally-joined row)
//   - \x07 (BEL) is dropped (no audible bell from inside the TUI)
//   - OSC sequences \x1b]...\x07 or \x1b]...\x1b\\ are dropped (window
//     title updates, hyperlinks)
//   - CSI sequences \x1b[<params><final> are dropped UNLESS the final
//     byte is 'm' (SGR — colour / bold / underline / etc., which we
//     want to keep)
//   - Lone ESC (followed by neither '[' nor ']') and other 2-byte ESC
//     sequences are dropped (cursor save/restore, charset selection,
//     etc. — none of which we want inside a side-by-side pane)
//
// The output is a normal Go string suitable for handing to lipgloss.
func sanitizePtyForViewport(b []byte) string {
	var out strings.Builder
	out.Grow(len(b))
	i := 0
	for i < len(b) {
		c := b[i]
		switch {
		case c == '\r':
			// Drop. \r\n becomes \n on the next iteration.
			i++
		case c == 0x07:
			// BEL — drop silently.
			i++
		case c == 0x1b && i+1 < len(b):
			next := b[i+1]
			switch next {
			case '[':
				// CSI sequence: \x1b[ <params> <final>. Parameter bytes
				// are 0x30-0x3F, intermediate bytes 0x20-0x2F, final
				// 0x40-0x7E.
				j := i + 2
				for j < len(b) && b[j] >= 0x20 && b[j] <= 0x3F {
					j++
				}
				for j < len(b) && b[j] >= 0x20 && b[j] <= 0x2F {
					j++
				}
				if j < len(b) && b[j] >= 0x40 && b[j] <= 0x7E {
					if b[j] == 'm' {
						// SGR — keep the whole sequence verbatim so
						// colour attributes flow through lipgloss
						// into the terminal.
						out.Write(b[i : j+1])
					}
					i = j + 1
				} else {
					// Malformed / truncated — bail and drop the ESC[.
					i = j
				}
			case ']':
				// OSC: \x1b] ... ST. Terminator is BEL (0x07) or
				// \x1b\\ (ST).
				j := i + 2
				for j < len(b) {
					if b[j] == 0x07 {
						i = j + 1
						break
					}
					if b[j] == 0x1b && j+1 < len(b) && b[j+1] == '\\' {
						i = j + 2
						break
					}
					j++
				}
				if j >= len(b) {
					// Unterminated OSC at buffer end — drop rest.
					i = len(b)
				}
			default:
				// Two-byte ESC sequence (charset, save/restore, etc.) — drop.
				i += 2
			}
		default:
			out.WriteByte(c)
			i++
		}
	}
	return out.String()
}
