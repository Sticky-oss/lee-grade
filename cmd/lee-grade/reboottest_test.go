package main

import (
	"testing"

	"github.com/sticky-oss/lee-grade/internal/check"
)

func TestClassifyReboot(t *testing.T) {
	cases := []struct {
		name                 string
		pre, post, evaluated bool
		want                 string
	}{
		{"passed before and after", true, true, true, statusPersisted},
		{"passed before, failed after", true, false, true, statusRegressed},
		{"failed before, passed after", false, true, true, statusRecovered},
		{"failed before and after", false, false, true, statusStillFailing},
		{"not re-evaluated (was passing)", true, true, false, statusUnknown},
		{"not re-evaluated (was failing)", false, false, false, statusUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyReboot(tc.pre, tc.post, tc.evaluated); got != tc.want {
				t.Errorf("classifyReboot(%v,%v,%v) = %q, want %q", tc.pre, tc.post, tc.evaluated, got, tc.want)
			}
		})
	}
}

func TestDiffRebootResults(t *testing.T) {
	st := rebootState{
		Pre: []*check.TaskResult{{
			TaskID: "t1",
			Title:  "Demo",
			Checks: []check.Result{
				{CheckID: "a", Description: "A", Passed: true},
				{CheckID: "b", Description: "B", Passed: true},
				{CheckID: "c", Description: "C", Passed: false},
				{CheckID: "d", Description: "D", Passed: true},
			},
		}},
	}
	// Post-reboot, in a different order than pre: b regressed, c recovered,
	// a persisted, and d was not re-evaluated (absent) → unknown.
	post := []*check.TaskResult{{
		TaskID: "t1",
		Title:  "Demo",
		Checks: []check.Result{
			{CheckID: "c", Passed: true},
			{CheckID: "a", Passed: true},
			{CheckID: "b", Passed: false, Detail: "boom"},
		},
	}}

	rep := diffRebootResults(st, post)

	if rep.Regressions != 1 {
		t.Fatalf("Regressions = %d, want 1", rep.Regressions)
	}
	if len(rep.Tasks) != 1 || len(rep.Tasks[0].Checks) != 4 {
		t.Fatalf("unexpected report shape: %+v", rep.Tasks)
	}
	want := map[string]string{
		"a": statusPersisted,
		"b": statusRegressed,
		"c": statusRecovered,
		"d": statusUnknown,
	}
	for _, cd := range rep.Tasks[0].Checks {
		if cd.Status != want[cd.CheckID] {
			t.Errorf("check %s: status = %q, want %q", cd.CheckID, cd.Status, want[cd.CheckID])
		}
		if cd.CheckID == "b" && cd.PostDetail != "boom" {
			t.Errorf("regressed check b: PostDetail = %q, want %q", cd.PostDetail, "boom")
		}
	}
}

func TestDiffRebootResults_TaskMissingPostReboot(t *testing.T) {
	// A whole task that couldn't be re-graded after reboot: every check is
	// "unknown", and a missing check must NOT be counted as a regression.
	st := rebootState{
		Pre: []*check.TaskResult{{
			TaskID: "gone",
			Checks: []check.Result{{CheckID: "x", Passed: true}},
		}},
	}
	rep := diffRebootResults(st, nil)
	if rep.Regressions != 0 {
		t.Fatalf("Regressions = %d, want 0 (missing post grade = unknown, not regressed)", rep.Regressions)
	}
	if got := rep.Tasks[0].Checks[0].Status; got != statusUnknown {
		t.Errorf("status = %q, want %q", got, statusUnknown)
	}
}
