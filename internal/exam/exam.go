// Package exam adds the timed-sitting layer on top of the task grader: an
// Exam is an ordered set of tasks graded as ONE exam against a cut score and
// a wall-clock time budget — the EX200/EX294 experience, where you get a host
// and a deadline and pass iff your aggregate score clears the bar.
//
// This package is deliberately I/O- and host-free: it loads the exam
// definition, tracks session State (start + deadline), and scores a set of
// per-task outcomes. The cmd layer owns grading (check.RunTask), state
// persistence, and rendering — so everything here is pure and unit-testable.
package exam

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// CurrentSchemaVersion is the latest exam-def schema this binary understands.
const CurrentSchemaVersion = 1

// Exam is the top-level exam-definition document (one Exam per YAML file).
type Exam struct {
	// SchemaVersion lets the exam DSL evolve. Missing == CurrentSchemaVersion.
	SchemaVersion int `yaml:"schema_version,omitempty"`

	// ID is a stable kebab-case identifier (e.g. "rhce-9-sample").
	ID string `yaml:"id"`

	// Title is the human-readable exam name shown in the brief.
	Title string `yaml:"title"`

	// Track distinguishes RHCSA-9 vs RHCE-294 etc. Display + filtering.
	Track string `yaml:"track,omitempty"`

	// TimeMinutes is the total wall-clock budget for the sitting. Required.
	TimeMinutes int `yaml:"time_minutes"`

	// PassPercent is the cut score as a whole-number percent (RHCSA/RHCE = 70).
	PassPercent int `yaml:"pass_percent"`

	// Tasks lists the task YAML files that make up the exam, in presentation
	// order. Paths are relative to the exam file's own directory (or absolute);
	// LoadExam resolves them so the caller gets ready-to-load paths.
	Tasks []string `yaml:"tasks"`
}

// LoadExam reads and validates an exam definition, resolving every task path
// relative to the exam file's directory so the returned Tasks are directly
// loadable by task.LoadFile.
func LoadExam(path string) (*Exam, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var e Exam
	if err := yaml.Unmarshal(data, &e); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := e.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	base := filepath.Dir(path)
	for i, ref := range e.Tasks {
		if !filepath.IsAbs(ref) {
			e.Tasks[i] = filepath.Join(base, ref)
		}
	}
	return &e, nil
}

// Validate catches the common authoring mistakes before a sitting starts.
func (e *Exam) Validate() error {
	if e.ID == "" {
		return fmt.Errorf("exam is missing required field 'id'")
	}
	if e.Title == "" {
		return fmt.Errorf("exam %q is missing required field 'title'", e.ID)
	}
	if e.SchemaVersion != 0 && e.SchemaVersion > CurrentSchemaVersion {
		return fmt.Errorf("exam %q declares schema_version=%d but this lee-grade understands at most %d",
			e.ID, e.SchemaVersion, CurrentSchemaVersion)
	}
	if e.TimeMinutes <= 0 {
		return fmt.Errorf("exam %q needs a positive time_minutes", e.ID)
	}
	if e.PassPercent < 1 || e.PassPercent > 100 {
		return fmt.Errorf("exam %q pass_percent must be 1..100, got %d", e.ID, e.PassPercent)
	}
	if len(e.Tasks) == 0 {
		return fmt.Errorf("exam %q lists no tasks", e.ID)
	}
	return nil
}

// State is the persisted record of an in-progress sitting. Written by
// `exam start`, read by `exam status` / `exam grade`.
type State struct {
	Version     int       `json:"version"`
	ExamID      string    `json:"exam_id"`
	ExamPath    string    `json:"exam_path"`
	Title       string    `json:"title"`
	Track       string    `json:"track,omitempty"`
	TaskPaths   []string  `json:"task_paths"`
	PassPercent int       `json:"pass_percent"`
	StartedAt   time.Time `json:"started_at"`
	Deadline    time.Time `json:"deadline"`
}

// NewState builds the session record for a freshly-started sitting.
func NewState(e *Exam, examPath string, now time.Time) State {
	return State{
		Version:     1,
		ExamID:      e.ID,
		ExamPath:    examPath,
		Title:       e.Title,
		Track:       e.Track,
		TaskPaths:   append([]string(nil), e.Tasks...),
		PassPercent: e.PassPercent,
		StartedAt:   now.UTC(),
		Deadline:    now.UTC().Add(time.Duration(e.TimeMinutes) * time.Minute),
	}
}

// Remaining is the time left until the deadline at `now`. It is negative once
// the sitting is in overtime; callers use OverBy/Sign to phrase that.
func (s State) Remaining(now time.Time) time.Duration {
	return s.Deadline.Sub(now)
}

// WithinTime reports whether `now` is at or before the deadline.
func (s State) WithinTime(now time.Time) bool {
	return !now.After(s.Deadline)
}

// TaskOutcome is the minimal per-task grading result the scorer needs — the
// cmd layer maps a check.TaskResult onto this so the exam package stays
// decoupled from the check engine (and its tests need no host).
type TaskOutcome struct {
	TaskID string
	Title  string
	Domain string
	Passed int
	Total  int
}

// TaskScore is one task's contribution to the exam score.
type TaskScore struct {
	TaskID  string
	Title   string
	Domain  string
	Passed  int
	Total   int
	Percent int
}

// Score is the aggregate exam result: partial credit by check across all
// tasks, compared to the cut score.
type Score struct {
	Tasks        []TaskScore
	PassedChecks int
	TotalChecks  int
	Percent      int
	PassPercent  int
	Passed       bool
}

// ScoreExam aggregates per-task outcomes into an exam score. Credit is by
// check (a task contributes all its passed checks), and the sitting passes iff
// the aggregate clears the cut score. The >= comparison is done in integer
// cross-multiplied form to avoid rounding the percent before the threshold
// test (e.g. 209/300 must not round up to a passing 70%).
func ScoreExam(outcomes []TaskOutcome, passPercent int) Score {
	sc := Score{PassPercent: passPercent, Tasks: make([]TaskScore, 0, len(outcomes))}
	for _, o := range outcomes {
		ts := TaskScore{TaskID: o.TaskID, Title: o.Title, Domain: o.Domain, Passed: o.Passed, Total: o.Total}
		if o.Total > 0 {
			ts.Percent = (o.Passed * 100) / o.Total
		}
		sc.Tasks = append(sc.Tasks, ts)
		sc.PassedChecks += o.Passed
		sc.TotalChecks += o.Total
	}
	if sc.TotalChecks > 0 {
		sc.Percent = (sc.PassedChecks * 100) / sc.TotalChecks
		sc.Passed = sc.PassedChecks*100 >= passPercent*sc.TotalChecks
	}
	return sc
}
