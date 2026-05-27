// grade.go runs check.RunTask asynchronously and delivers the result
// back into the bubbletea Update loop. Re-grading is triggered every
// time the user presses Enter in the right pane (a guess that they just
// finished a command and the host state may have changed), and on
// explicit Ctrl+G.
//
// Why async: some checks shell out (run `id alice` etc.) and can take
// 50-300ms on a busy box. Doing this synchronously in Update would
// stall the render frame — typing feels laggy.
package tui

import (
	"github.com/sticky-oss/lee-grade/internal/check"
	"github.com/sticky-oss/lee-grade/internal/task"

	tea "github.com/charmbracelet/bubbletea"
)

// gradeResultMsg delivers a finished TaskResult back into Update. The
// model swaps its current Result, which the View picks up next frame.
type gradeResultMsg struct {
	result *check.TaskResult
}

// gradeCmd returns a tea.Cmd that runs check.RunTask against the current
// host state and posts the result. Cheap to call repeatedly — the check
// engine is stateless.
func gradeCmd(t *task.Task) tea.Cmd {
	return func() tea.Msg {
		return gradeResultMsg{result: check.RunTask(t)}
	}
}
