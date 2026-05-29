package check

import (
	"testing"

	"github.com/sticky-oss/lee-grade/internal/task"
	"gopkg.in/yaml.v3"
)

// loadCheck builds a *task.Check from a one-off YAML snippet. Keeps test
// bodies focused on the assertion rather than YAML scaffolding.
func loadCheck(t *testing.T, yamlText string) *task.Check {
	t.Helper()
	var c task.Check
	if err := yaml.Unmarshal([]byte(yamlText), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return &c
}

func TestRunTask_unknownCheckTypeProducesErrorResultNotPanic(t *testing.T) {
	tr := RunTask(&task.Task{
		ID:    "t-unknown",
		Title: "x",
		Checks: []task.Check{{
			ID:          "c1",
			Description: "checks something nonexistent",
			Type:        "definitely-not-a-real-type",
		}},
	})
	if tr.Passed != 0 || tr.Total != 1 {
		t.Errorf("passed/total wrong: %+v", tr)
	}
	if tr.Checks[0].Error == "" {
		t.Errorf("expected Error to be populated for unknown type")
	}
}

func TestRunTask_fileCheckExistsAgainstPasswd(t *testing.T) {
	// /etc/passwd exists on every Linux system we care about. Use it as a
	// pin so the test is hermetic (no fixture needed).
	c := loadCheck(t, `
id: c1
description: /etc/passwd exists
type: file
path: /etc/passwd
`)
	tr := RunTask(&task.Task{
		ID:     "t1",
		Title:  "passwd test",
		Checks: []task.Check{*c},
	})
	if !tr.FullyPassed() {
		t.Errorf("expected full pass, got %+v (detail: %q)", tr, tr.Checks[0].Detail)
	}
}

func TestRunTask_fileCheckAbsenceOfNonexistentFile(t *testing.T) {
	c := loadCheck(t, `
id: c1
description: nonexistent file is absent
type: file
path: /etc/this-file-truly-does-not-exist
exists: false
`)
	tr := RunTask(&task.Task{
		ID:     "t1",
		Title:  "absence test",
		Checks: []task.Check{*c},
	})
	if !tr.FullyPassed() {
		t.Errorf("expected full pass (absence), got %+v", tr)
	}
}

func TestRunTask_userRootExists(t *testing.T) {
	c := loadCheck(t, `
id: c1
description: root user exists
type: user
name: root
uid: 0
`)
	tr := RunTask(&task.Task{
		ID:     "t1",
		Title:  "root test",
		Checks: []task.Check{*c},
	})
	if !tr.FullyPassed() {
		t.Errorf("expected root to exist with UID 0, got %+v (detail: %q)", tr, tr.Checks[0].Detail)
	}
}

func TestRunTask_nonexistentUserFailsCleanly(t *testing.T) {
	c := loadCheck(t, `
id: c1
description: ghost user exists
type: user
name: this-user-does-not-exist-12345
`)
	tr := RunTask(&task.Task{
		ID:     "t1",
		Title:  "ghost test",
		Checks: []task.Check{*c},
	})
	if tr.FullyPassed() {
		t.Errorf("expected failure for nonexistent user")
	}
	if tr.Checks[0].Detail == "" {
		t.Errorf("expected Detail to explain WHY (not just Passed=false)")
	}
}

func TestRunTask_percentCalculation(t *testing.T) {
	// One pass, one fail → 50%.
	tr := RunTask(&task.Task{
		ID:    "t1",
		Title: "x",
		Checks: []task.Check{
			*loadCheck(t, `{id: c1, description: passes, type: file, path: /etc/passwd}`),
			*loadCheck(t, `{id: c2, description: fails, type: file, path: /etc/this-does-not-exist}`),
		},
	})
	if tr.Passed != 1 || tr.Total != 2 || tr.Percent != 50 {
		t.Errorf("expected 1/2 = 50%%, got %d/%d = %d%%", tr.Passed, tr.Total, tr.Percent)
	}
}

func TestRegisteredTypes_includesCoreSet(t *testing.T) {
	types := RegisteredTypes()
	want := []string{"file", "file-content", "user", "group", "user-in-group", "service-state", "package-installed", "mount", "selinux", "firewall", "cron-job", "ansible-playbook"}
	got := make(map[string]bool, len(types))
	for _, t := range types {
		got[t] = true
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("type %q not registered (have: %v)", w, types)
		}
	}
}
