package render

import (
	"bytes"
	"strings"
	"testing"

	"github.com/sticky-oss/lee-grade/internal/check"
)

func TestHuman_noAnsiWhenDisabled(t *testing.T) {
	AnsiSupported = false
	var b bytes.Buffer
	tr := &check.TaskResult{
		TaskID: "t1", Title: "Demo", Total: 2, Passed: 1, Percent: 50,
		Checks: []check.Result{
			{CheckID: "a", Description: "alpha ok", Passed: true},
			{CheckID: "b", Description: "bravo bad", Passed: false, Detail: "nope", Hint: "do x"},
		},
	}
	Human(&b, tr)
	out := b.String()
	if strings.Contains(out, "\x1b") {
		t.Errorf("no ANSI expected when AnsiSupported=false:\n%q", out)
	}
	if !strings.Contains(out, "✓ alpha ok") || !strings.Contains(out, "✗ bravo bad") {
		t.Errorf("icon/description missing:\n%s", out)
	}
	if !strings.Contains(out, "hint: do x") || !strings.Contains(out, "1 / 2 checks passed (50%)") {
		t.Errorf("hint/summary missing:\n%s", out)
	}
}

func TestHuman_sanitizesControlChars(t *testing.T) {
	AnsiSupported = false
	var b bytes.Buffer
	tr := &check.TaskResult{
		TaskID: "t", Title: "T", Total: 1,
		Checks: []check.Result{
			{CheckID: "x", Description: "line1\nline2\x1b[31mINJECT", Passed: false, Detail: "det\rail"},
		},
	}
	Human(&b, tr)
	out := b.String()
	if strings.Contains(out, "\x1b") || strings.Contains(out, "line1\nline2") {
		t.Errorf("control chars / raw ESC not stripped from untrusted fields:\n%q", out)
	}
}

func TestClean(t *testing.T) {
	// Control bytes (newline, tab, ESC, DEL) are dropped; printable bytes —
	// including the bytes that *followed* an ESC — are kept (harmless once the
	// ESC itself is gone, so no ANSI is interpreted).
	if got := clean("a\nb\tc\x1bd\x7f"); got != "abcd" {
		t.Errorf("clean = %q, want \"abcd\"", got)
	}
}

func TestJSON(t *testing.T) {
	var b bytes.Buffer
	tr := &check.TaskResult{
		TaskID: "t", Title: "T", Total: 1, Passed: 1, Percent: 100,
		Checks: []check.Result{{CheckID: "a", Description: "d", Passed: true}},
	}
	if err := JSON(&b, tr); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, `"task_id": "t"`) || !strings.Contains(out, `"passed": 1`) {
		t.Errorf("unexpected JSON:\n%s", out)
	}
}
