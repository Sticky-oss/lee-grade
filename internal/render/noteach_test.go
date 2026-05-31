package render

import (
	"bytes"
	"strings"
	"testing"

	"github.com/sticky-oss/lee-grade/internal/check"
)

// In --no-teach (challenge) mode a failed check must reveal nothing that helps:
// no actual-state detail, no why, no command hint — only the verdict.
func TestHuman_noTeachHidesCoaching(t *testing.T) {
	prevAnsi, prevTeach := AnsiSupported, ShowTeaching
	AnsiSupported, ShowTeaching = false, false
	defer func() { AnsiSupported, ShowTeaching = prevAnsi, prevTeach }()

	tr := &check.TaskResult{
		TaskID: "t1", Title: "Demo", Passed: 0, Total: 1, Percent: 0,
		Checks: []check.Result{{
			Description: "thing is configured",
			Passed:      false,
			Detail:      "secret actual state",
			Why:         "the underlying concept",
			Hint:        "run the-command",
		}},
	}
	var buf bytes.Buffer
	Human(&buf, tr)
	out := buf.String()

	for _, leak := range []string{"secret actual state", "why:", "the underlying concept", "hint:", "the-command"} {
		if strings.Contains(out, leak) {
			t.Errorf("no-teach output leaked %q:\n%s", leak, out)
		}
	}
	if !strings.Contains(out, "thing is configured") {
		t.Errorf("the check description should still show:\n%s", out)
	}
	if !strings.Contains(out, "0 / 1 checks passed") {
		t.Errorf("the score line should still show:\n%s", out)
	}
}
