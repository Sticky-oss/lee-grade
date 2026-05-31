package main

// --describe: print a task's brief — its scenario and the objectives that will
// be graded — without touching the host. This is what `lab start` shows so a
// learner knows exactly what to do, mirroring the scenario panel in lee-lab.

import (
	"fmt"
	"io"
	"strings"

	"github.com/sticky-oss/lee-grade/internal/task"
)

// describeBrief renders a boxed assignment brief: title + domain/time, the
// scenario description, then the per-check objectives as a checklist.
func describeBrief(w io.Writer, t *task.Task, color bool) {
	const (
		cyan   = "\x1b[36m"
		dim    = "\x1b[90m"
		green  = "\x1b[32m"
		yellow = "\x1b[33m"
		bold   = "\x1b[1m"
	)
	header := "FIELD ASSIGNMENT · " + t.ID
	fmt.Fprintln(w, col(color, cyan, "┌─ "+header+" "+strings.Repeat("─", max(0, 70-3-len(header)))+"┐"))

	meta := t.Domain
	if t.TimeMinutes > 0 {
		meta = fmt.Sprintf("%s · ~%d min", t.Domain, t.TimeMinutes)
	}
	fmt.Fprintln(w, col(color, cyan, "│ ")+col(color, bold, t.Title)+"  "+col(color, dim, "["+meta+"]"))
	fmt.Fprintln(w, col(color, cyan, "│"))

	for _, line := range strings.Split(strings.TrimRight(t.Description, "\n"), "\n") {
		fmt.Fprintln(w, col(color, cyan, "│ ")+line)
	}

	fmt.Fprintln(w, col(color, cyan, "│"))
	fmt.Fprintln(w, col(color, cyan, "│ ")+col(color, yellow, "OBJECTIVES")+col(color, dim, "  (these are what `grade` checks)"))
	for _, c := range t.Checks {
		fmt.Fprintln(w, col(color, cyan, "│ ")+col(color, green, "  ▢ ")+c.Description)
	}
	fmt.Fprintln(w, col(color, cyan, "└"+strings.Repeat("─", 69)+"┘"))
}
