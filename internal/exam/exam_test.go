package exam

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadExam_resolvesTaskPathsRelativeToFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.yaml")
	body := `
schema_version: 1
id: rhce-9-sample
title: RHCE sample
track: rhce-9
time_minutes: 90
pass_percent: 70
tasks:
  - tasks/a.yaml
  - /abs/b.yaml
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := LoadExam(path)
	if err != nil {
		t.Fatalf("LoadExam: %v", err)
	}
	wantRel := filepath.Join(dir, "tasks", "a.yaml")
	if e.Tasks[0] != wantRel {
		t.Errorf("relative task path = %q, want %q", e.Tasks[0], wantRel)
	}
	if e.Tasks[1] != "/abs/b.yaml" {
		t.Errorf("absolute task path should be left as-is, got %q", e.Tasks[1])
	}
}

func TestExam_Validate(t *testing.T) {
	base := Exam{ID: "x", Title: "X", TimeMinutes: 90, PassPercent: 70, Tasks: []string{"a.yaml"}}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid exam rejected: %v", err)
	}
	cases := map[string]func(*Exam){
		"no id":          func(e *Exam) { e.ID = "" },
		"no title":       func(e *Exam) { e.Title = "" },
		"zero time":      func(e *Exam) { e.TimeMinutes = 0 },
		"bad pass low":   func(e *Exam) { e.PassPercent = 0 },
		"bad pass high":  func(e *Exam) { e.PassPercent = 101 },
		"no tasks":       func(e *Exam) { e.Tasks = nil },
		"future schema":  func(e *Exam) { e.SchemaVersion = CurrentSchemaVersion + 1 },
	}
	for name, mutate := range cases {
		e := base
		mutate(&e)
		if err := e.Validate(); err == nil {
			t.Errorf("%s: expected validation error, got nil", name)
		}
	}
}

func TestNewState_deadlineAndRemaining(t *testing.T) {
	e := &Exam{ID: "x", Title: "X", TimeMinutes: 90, PassPercent: 70, Tasks: []string{"a.yaml"}}
	start := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)
	st := NewState(e, "/exams/x.yaml", start)

	if got := st.Deadline.Sub(st.StartedAt); got != 90*time.Minute {
		t.Errorf("deadline-start = %v, want 90m", got)
	}
	// 30 minutes in: 60 left, within time.
	at := start.Add(30 * time.Minute)
	if got := st.Remaining(at); got != 60*time.Minute {
		t.Errorf("Remaining at +30m = %v, want 60m", got)
	}
	if !st.WithinTime(at) {
		t.Error("should be within time at +30m")
	}
	// 2h in: overtime by 30m.
	over := start.Add(120 * time.Minute)
	if got := st.Remaining(over); got != -30*time.Minute {
		t.Errorf("Remaining at +120m = %v, want -30m", got)
	}
	if st.WithinTime(over) {
		t.Error("should be in overtime at +120m")
	}
	// Exactly at the deadline still counts as within time.
	if !st.WithinTime(st.Deadline) {
		t.Error("the deadline instant should count as within time")
	}
}

func TestScoreExam_aggregateAndCutScore(t *testing.T) {
	outcomes := []TaskOutcome{
		{TaskID: "t1", Title: "One", Passed: 4, Total: 4},
		{TaskID: "t2", Title: "Two", Passed: 1, Total: 3},
		{TaskID: "t3", Title: "Three", Passed: 0, Total: 3},
	}
	sc := ScoreExam(outcomes, 70)
	if sc.PassedChecks != 5 || sc.TotalChecks != 10 {
		t.Fatalf("aggregate = %d/%d, want 5/10", sc.PassedChecks, sc.TotalChecks)
	}
	if sc.Percent != 50 {
		t.Errorf("percent = %d, want 50", sc.Percent)
	}
	if sc.Passed {
		t.Error("50%% should not pass a 70%% cut")
	}
	if sc.Tasks[1].Percent != 33 {
		t.Errorf("t2 per-task percent = %d, want 33", sc.Tasks[1].Percent)
	}
}

func TestScoreExam_cutScoreIsNotRoundedUp(t *testing.T) {
	// 209/300 = 69.67%. Displayed percent floors to 69; the pass test must
	// also fail (it must not round 69.67 up to a passing 70).
	sc := ScoreExam([]TaskOutcome{{TaskID: "t", Passed: 209, Total: 300}}, 70)
	if sc.Percent != 69 {
		t.Errorf("percent = %d, want 69", sc.Percent)
	}
	if sc.Passed {
		t.Error("209/300 must not pass a 70%% cut")
	}
	// 210/300 = exactly 70% → passes.
	sc = ScoreExam([]TaskOutcome{{TaskID: "t", Passed: 210, Total: 300}}, 70)
	if !sc.Passed {
		t.Error("210/300 should pass a 70%% cut")
	}
}

func TestScoreExam_emptyIsNotAPass(t *testing.T) {
	sc := ScoreExam(nil, 70)
	if sc.Passed {
		t.Error("an exam with no checks must not pass")
	}
}
