// Package task defines the lee-grade Task DSL: the shared task-definition
// schema consumed by both lee-grade (this Go CLI, grades real RHEL boxes)
// and lee-lab (the browser sim).
//
// A Task is a unit of work — a single RHCSA scenario — with a list of
// declarative grading checks. Each Check has a `type` discriminator (one
// of the strings registered in internal/check) and a free-form arguments
// map that the corresponding check implementation knows how to interpret.
//
// The DSL is YAML on disk for human authoring; this package parses it
// into typed Go structs. The check-args map is kept as `yaml.Node` so the
// per-type checker decodes its own arguments — this keeps Task itself
// agnostic to the growing check-type alphabet.
package task

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// CurrentSchemaVersion is the latest DSL schema version this binary
// understands. Tasks that declare a higher version are rejected; lower
// versions are accepted with a best-effort upgrade where possible.
const CurrentSchemaVersion = 1

// Task is the top-level YAML document. Every task file is one Task.
type Task struct {
	// SchemaVersion lets us evolve the DSL without breaking old task files.
	// Optional — missing == CurrentSchemaVersion.
	SchemaVersion int `yaml:"schema_version,omitempty"`

	// ID is a stable identifier: kebab-case, repo-unique. Used by --task on
	// the CLI and as the key for any persisted grade results.
	ID string `yaml:"id"`

	// Title is a short human-readable name shown in the result panel.
	Title string `yaml:"title"`

	// Domain is the EX200/EX294 objective family this task exercises
	// (e.g. "user-group-management", "storage-lvm", "firewall"). Used for
	// grouping in summary output.
	Domain string `yaml:"domain"`

	// Track distinguishes RHCSA-9 vs RHCE-294 vs other curricula. lee-grade
	// uses it for filtering with --track on the CLI.
	Track string `yaml:"track,omitempty"`

	// TimeMinutes is the suggested wall-clock budget for this task during
	// exam-day practice. Display-only.
	TimeMinutes int `yaml:"time_minutes,omitempty"`

	// Description is markdown shown to the learner when they open the task.
	// Multi-line `|` block scalars are the expected form.
	Description string `yaml:"description"`

	// Checks is the ordered list of grading checks. Order matters only for
	// presentation; checks are independent.
	Checks []Check `yaml:"checks"`
}

// Check is one grading check inside a Task. The `Type` discriminator
// selects an implementation registered in internal/check; `Args` absorbs
// every OTHER YAML key at this level so each implementation can decode
// its own typed argument struct without coupling Check itself to the
// growing alphabet of check types.
type Check struct {
	// ID is a stable identifier within the task — used in JSON output and
	// in saved grade results so a check can be referenced across runs.
	ID string `yaml:"id"`

	// Description is what's shown next to the ✓ / ✗ in the result panel.
	// Phrase it as the END STATE being verified, e.g. "User alice exists
	// with UID 2001" — not "The grader checks that user alice…".
	Description string `yaml:"description"`

	// Hint is the canonical command (or short prose) shown on a failed
	// check. Should be the answer for the canonical solve path. Real-shell
	// verisimilitude matters here — write the exact command the learner
	// would run.
	Hint string `yaml:"hint,omitempty"`

	// Type is the check-type discriminator. Must match a registered impl.
	Type string `yaml:"type"`

	// Host optionally targets a managed node by logical name (resolved from
	// the --hosts file) instead of the local host — used to grade per-node
	// state in a multi-node topology. Empty means "the local host". Only
	// remote-capable check types honour it; others error rather than silently
	// grading localhost.
	Host string `yaml:"host,omitempty"`

	// Args absorbs every sibling YAML key not matched above (path, mode,
	// owner, name, uid, …). The per-type implementation decodes whatever
	// subset it cares about. Inline + map[string]any is the yaml.v3
	// idiom for "open struct" extension points.
	Args map[string]any `yaml:",inline"`
}

// DecodeArgs unmarshals the open Args map into the caller's typed struct
// by round-tripping through YAML. The marshal/unmarshal is necessary
// because gopkg.in/yaml.v3 only populates struct fields when decoding
// from a YAML node — it doesn't reflect typed structs out of a generic
// map. Cost is negligible (the map is tiny: ~5 keys).
func (c *Check) DecodeArgs(out any) error {
	data, err := yaml.Marshal(c.Args)
	if err != nil {
		return fmt.Errorf("check %q: marshal args: %w", c.ID, err)
	}
	// KnownFields(true) rejects any key the target arg struct doesn't define, so
	// a misspelled argument (e.g. `onwer:` for `owner:`) errors loudly instead
	// of silently decoding to the zero value and grading wrong.
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("check %q: invalid arguments: %w", c.ID, err)
	}
	return nil
}

// Validate runs a structural sanity check on a parsed Task. Catches the
// common authoring mistakes (missing id, no checks, duplicate check ids)
// before the runner tries to execute it.
func (t *Task) Validate() error {
	if t.ID == "" {
		return fmt.Errorf("task is missing required field 'id'")
	}
	if t.Title == "" {
		return fmt.Errorf("task %q is missing required field 'title'", t.ID)
	}
	if len(t.Checks) == 0 {
		return fmt.Errorf("task %q has no checks", t.ID)
	}
	if t.SchemaVersion < 0 || t.SchemaVersion > CurrentSchemaVersion {
		return fmt.Errorf(
			"task %q declares schema_version=%d but this lee-grade understands 1..%d",
			t.ID, t.SchemaVersion, CurrentSchemaVersion,
		)
	}
	seen := make(map[string]bool, len(t.Checks))
	for i, c := range t.Checks {
		if c.ID == "" {
			return fmt.Errorf("task %q check #%d is missing required field 'id'", t.ID, i+1)
		}
		if c.Type == "" {
			return fmt.Errorf("task %q check %q is missing required field 'type'", t.ID, c.ID)
		}
		if c.Description == "" {
			return fmt.Errorf("task %q check %q is missing required field 'description'", t.ID, c.ID)
		}
		if seen[c.ID] {
			return fmt.Errorf("task %q has duplicate check id %q", t.ID, c.ID)
		}
		seen[c.ID] = true
	}
	return nil
}
