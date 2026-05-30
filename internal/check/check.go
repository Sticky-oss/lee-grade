// Package check is the check-execution engine: a registry of typed check
// implementations + a Run() entry that dispatches each Task.Check to the
// right implementation.
//
// To add a new check type, implement the Checker interface and call
// Register("my-type", &MyChecker{}) in your file's init().
package check

import (
	"fmt"

	"github.com/sticky-oss/lee-grade/internal/task"
)

// Result is one check's outcome. Multiple Results compose into a TaskResult.
type Result struct {
	// CheckID matches Task.Checks[i].ID.
	CheckID string `json:"check_id"`
	// Description is a copy of Task.Checks[i].Description, included so JSON
	// consumers don't need the source task alongside the result.
	Description string `json:"description"`
	// Passed is true iff the check verified its end state.
	Passed bool `json:"passed"`
	// Hint is shown on failure — the canonical fix command, copied from
	// Task.Checks[i].Hint when Passed is false. Empty on success.
	Hint string `json:"hint,omitempty"`
	// Detail is a human-readable diagnostic ("file has mode 0644, want 0600")
	// surfaced on failure. Implementations should populate this when it
	// would help the learner understand why the check failed.
	Detail string `json:"detail,omitempty"`
	// Error is populated only when the check itself errored (couldn't
	// inspect state — e.g. permission denied on /etc/shadow). Distinct
	// from "ran cleanly and the assertion failed".
	Error string `json:"error,omitempty"`
}

// TaskResult aggregates a Task and its check results into one summary.
type TaskResult struct {
	TaskID    string   `json:"task_id"`
	Title     string   `json:"title"`
	Domain    string   `json:"domain"`
	Track     string   `json:"track,omitempty"`
	Passed    int      `json:"passed"`
	Total     int      `json:"total"`
	Percent   int      `json:"percent"`
	Checks    []Result `json:"checks"`
}

// FullyPassed reports whether every check passed (and there is at least
// one check). Used for "did the learner complete this task?" decisions.
func (r *TaskResult) FullyPassed() bool {
	return r.Total > 0 && r.Passed == r.Total
}

// Checker is the interface every check type implements. Run inspects the
// host state and returns whether the check's assertion holds. Returning
// an error indicates a system-inspection failure (couldn't read a file,
// command not found, etc.) — the runner converts this to a Result with
// Error set, not a hard abort.
type Checker interface {
	// Run executes the check against the host, decoding its typed
	// arguments from the inline YAML attached to Task.Check.Args.
	Run(c *task.Check) Result
}

// CheckerFunc is an adapter that turns an ordinary function into a Checker
// — convenient for simple check types that don't need state.
type CheckerFunc func(c *task.Check) Result

// Run satisfies Checker.
func (f CheckerFunc) Run(c *task.Check) Result { return f(c) }

// registry maps check type strings to their implementations. Populated
// at init() time by each per-type file.
var registry = map[string]Checker{}

// Register attaches an implementation to a check type name. Panics on
// duplicate registration — a registration collision is a programmer
// error, not a runtime fault.
func Register(name string, c Checker) {
	if _, exists := registry[name]; exists {
		panic(fmt.Sprintf("check: type %q already registered", name))
	}
	registry[name] = c
}

// RegisteredTypes returns the alphabet of currently-known check types.
// Useful for --list-check-types on the CLI and for tests that want to
// assert "every check type referenced in tasks/ has an implementation".
func RegisteredTypes() []string {
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	return out
}

// RunTask grades a whole task by dispatching each check to its registered
// implementation. An unknown check type produces a Result with Error set
// (not a panic) so partial grading still works when a task references a
// future check type this binary doesn't know about yet.
func RunTask(t *task.Task) *TaskResult {
	tr := &TaskResult{
		TaskID: t.ID,
		Title:  t.Title,
		Domain: t.Domain,
		Track:  t.Track,
		Total:  len(t.Checks),
		Checks: make([]Result, 0, len(t.Checks)),
	}
	for i := range t.Checks {
		c := &t.Checks[i]
		impl, ok := registry[c.Type]
		if !ok {
			tr.Checks = append(tr.Checks, Result{
				CheckID:     c.ID,
				Description: c.Description,
				Passed:      false,
				Hint:        c.Hint,
				Error:       fmt.Sprintf("unknown check type %q (registered: %v)", c.Type, RegisteredTypes()),
			})
			continue
		}
		// Guard against silently grading localhost: a `host` on a check type
		// that can't run remotely yet is an authoring error, not a pass.
		if c.Host != "" && !remoteCapable[c.Type] {
			tr.Checks = append(tr.Checks, Result{
				CheckID:     c.ID,
				Description: c.Description,
				Passed:      false,
				Hint:        c.Hint,
				Error:       fmt.Sprintf("check type %q does not support a remote 'host' yet (supported: service-state, package-installed, file-content)", c.Type),
			})
			continue
		}
		r := impl.Run(c)
		// Implementation may forget to copy these — fill in for safety.
		if r.CheckID == "" {
			r.CheckID = c.ID
		}
		if r.Description == "" {
			r.Description = c.Description
		}
		if !r.Passed && r.Hint == "" {
			r.Hint = c.Hint
		}
		tr.Checks = append(tr.Checks, r)
		if r.Passed {
			tr.Passed++
		}
	}
	if tr.Total > 0 {
		tr.Percent = (tr.Passed * 100) / tr.Total
	}
	return tr
}
