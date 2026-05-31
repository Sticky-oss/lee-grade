// Package render formats TaskResults for terminal display or machine
// consumption. Two output modes today: "human" (boxed colored text) and
// "json" (one JSON object per task on stdout).
package render

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/sticky-oss/lee-grade/internal/check"
)

// AnsiSupported is set by main() based on whether the output destination
// is a TTY and the user hasn't passed --no-color. When false, all colour
// helpers below emit empty strings — keeps the JSON / piped-output paths
// clean.
var AnsiSupported = false

const (
	fg     = "\x1b[39m"
	dim    = "\x1b[90m"
	red    = "\x1b[31m"
	green  = "\x1b[32m"
	yellow = "\x1b[33m"
	blue   = "\x1b[34m"
	bold   = "\x1b[1m"
	reset  = "\x1b[0m"
)

func c(code string) string {
	if !AnsiSupported {
		return ""
	}
	return code
}

// Human prints a boxed per-check summary to w. The shape:
//
//   ┌─ Task 17 · Persistent firewall rule (firewall) ──────────────┐
//   │ ✓ Firewall allows http (runtime)                             │
//   │ ✓ Firewall allows http (permanent)                           │
//   │ ✗ Rule survives reboot                                       │
//   │     hint: did you forget --permanent before --reload?        │
//   │                                                              │
//   │ 2 / 3 checks passed                                          │
//   └──────────────────────────────────────────────────────────────┘
func Human(w io.Writer, tr *check.TaskResult) {
	header := clean(fmt.Sprintf("Task %s · %s", tr.TaskID, tr.Title))
	if tr.Domain != "" {
		header += " (" + clean(tr.Domain) + ")"
	}
	const innerWidth = 70
	// Width is rune-counted, not byte-counted, so a multibyte title doesn't
	// throw off the right-border alignment.
	fmt.Fprintln(w, c(blue)+"┌─ "+header+" "+strings.Repeat("─", max(0, innerWidth-3-utf8.RuneCountInString(header)))+"┐"+c(reset))
	for _, r := range tr.Checks {
		var icon, colour string
		switch {
		case r.Error != "":
			icon, colour = "!", c(yellow)
		case r.Passed:
			icon, colour = "✓", c(green)
		default:
			icon, colour = "✗", c(red)
		}
		fmt.Fprintln(w, c(blue)+"│ "+c(reset)+colour+icon+c(reset)+" "+clean(r.Description))
		if r.Detail != "" && !r.Passed {
			fmt.Fprintln(w, c(blue)+"│ "+c(reset)+"    "+c(dim)+clean(r.Detail)+c(reset))
		}
		if r.Error != "" {
			fmt.Fprintln(w, c(blue)+"│ "+c(reset)+"    "+c(yellow)+"error: "+clean(r.Error)+c(reset))
		}
		if !r.Passed && r.Hint != "" {
			fmt.Fprintln(w, c(blue)+"│ "+c(reset)+"    "+c(dim)+"hint: "+c(reset)+clean(r.Hint))
		}
	}
	fmt.Fprintln(w, c(blue)+"│"+c(reset))
	summaryColour := c(green)
	if tr.Passed < tr.Total {
		summaryColour = c(yellow)
	}
	if tr.Passed == 0 && tr.Total > 0 {
		summaryColour = c(red)
	}
	summary := fmt.Sprintf("%d / %d checks passed (%d%%)", tr.Passed, tr.Total, tr.Percent)
	fmt.Fprintln(w, c(blue)+"│ "+c(reset)+summaryColour+summary+c(reset))
	fmt.Fprintln(w, c(blue)+"└"+strings.Repeat("─", innerWidth-1)+"┘"+c(reset))
}

// JSON serialises a TaskResult as a single JSON document terminated by a
// newline. Use this for CI integrations and instructor dashboards.
func JSON(w io.Writer, tr *check.TaskResult) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(tr)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// clean strips control characters (newlines, tabs, raw ESC/DEL, …) from
// check-supplied strings so output captured from a host (e.g. a `command`
// check's Detail) can't break the box framing or inject ANSI even under
// --no-color.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
}
