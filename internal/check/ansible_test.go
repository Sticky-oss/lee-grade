package check

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubAnsible swaps the runAnsiblePlaybook seam for the duration of a test,
// the same pattern firewall/cron tests use for runCmd.
func stubAnsible(t *testing.T, fn func(dir, inventory, playbook string) (string, error)) {
	t.Helper()
	orig := runAnsiblePlaybook
	runAnsiblePlaybook = fn
	t.Cleanup(func() { runAnsiblePlaybook = orig })
}

// recapOut builds a single-host PLAY RECAP block with the given counters.
func recapOut(ok, changed, unreachable, failed int) string {
	return fmt.Sprintf(
		"PLAY RECAP ***************************\nlocalhost : ok=%d changed=%d unreachable=%d failed=%d skipped=0 rescued=0 ignored=0\n",
		ok, changed, unreachable, failed,
	)
}

// tempPlaybook writes a throwaway playbook file so the existence pre-check
// passes and the test reaches the (stubbed) run loop.
func tempPlaybook(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "play.yml")
	if err := os.WriteFile(p, []byte("---\n- hosts: all\n"), 0o644); err != nil {
		t.Fatalf("write temp playbook: %v", err)
	}
	return p
}

func TestParseRecap(t *testing.T) {
	t.Run("single host", func(t *testing.T) {
		out := `
PLAY [web] *********************************************************************

TASK [Gathering Facts] *********************************************************
ok: [localhost]

PLAY RECAP *********************************************************************
localhost                  : ok=4    changed=2    unreachable=0    failed=0    skipped=1    rescued=0    ignored=0
`
		r, ok := parseRecap(out)
		if !ok {
			t.Fatal("expected a recap to be found")
		}
		if r.Ok != 4 || r.Changed != 2 || r.Unreachable != 0 || r.Failed != 0 {
			t.Errorf("counters wrong: %+v", r)
		}
	})

	t.Run("multi host sums across nodes", func(t *testing.T) {
		out := `PLAY RECAP *********************************************************************
node1 : ok=3 changed=1 unreachable=0 failed=0 skipped=0 rescued=0 ignored=0
node2 : ok=2 changed=0 unreachable=1 failed=2 skipped=0 rescued=0 ignored=0`
		r, ok := parseRecap(out)
		if !ok {
			t.Fatal("expected a recap to be found")
		}
		if r.Ok != 5 || r.Changed != 1 || r.Unreachable != 1 || r.Failed != 2 {
			t.Errorf("multi-host sums wrong: %+v", r)
		}
	})

	t.Run("no recap present", func(t *testing.T) {
		if _, ok := parseRecap("ERROR! the playbook: web.yml could not be found"); ok {
			t.Error("expected ok=false when output has no PLAY RECAP")
		}
	})
}

func TestAnsiblePlaybook_idempotentPass(t *testing.T) {
	pb := tempPlaybook(t)
	calls := 0
	stubAnsible(t, func(_, _, _ string) (string, error) {
		calls++
		if calls == 1 {
			return recapOut(5, 2, 0, 0), nil // first run converges (changes)
		}
		return recapOut(5, 0, 0, 0), nil // second run is a no-op
	})
	r := checkAnsiblePlaybook(loadCheck(t, fmt.Sprintf(`{id: c, description: pb, type: ansible-playbook, playbook: %q}`, pb)))
	if !r.Passed {
		t.Errorf("converging + idempotent playbook should pass, got %+v", r)
	}
	if calls != 2 {
		t.Errorf("idempotency check should run the playbook twice, ran %d", calls)
	}
}

func TestAnsiblePlaybook_notIdempotentFails(t *testing.T) {
	pb := tempPlaybook(t)
	stubAnsible(t, func(_, _, _ string) (string, error) {
		return recapOut(5, 2, 0, 0), nil // every run reports changes
	})
	r := checkAnsiblePlaybook(loadCheck(t, fmt.Sprintf(`{id: c, description: pb, type: ansible-playbook, playbook: %q}`, pb)))
	if r.Passed {
		t.Errorf("a playbook that changes on every run is not idempotent and should fail, got %+v", r)
	}
	if !strings.Contains(r.Detail, "idempotent") {
		t.Errorf("Detail should explain the idempotency failure, got %q", r.Detail)
	}
}

func TestAnsiblePlaybook_failedTasksFail(t *testing.T) {
	pb := tempPlaybook(t)
	stubAnsible(t, func(_, _, _ string) (string, error) {
		return recapOut(3, 0, 0, 1), nil // a task failed
	})
	r := checkAnsiblePlaybook(loadCheck(t, fmt.Sprintf(`{id: c, description: pb, type: ansible-playbook, playbook: %q}`, pb)))
	if r.Passed {
		t.Errorf("failed=1 should fail the check, got %+v", r)
	}
	if !strings.Contains(r.Detail, "failed=1") {
		t.Errorf("Detail should report the failed count, got %q", r.Detail)
	}
}

func TestAnsiblePlaybook_missingPlaybookIsCleanFail(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.yml")
	stubAnsible(t, func(_, _, _ string) (string, error) {
		t.Fatal("runAnsiblePlaybook must not be called when the playbook is absent")
		return "", nil
	})
	r := checkAnsiblePlaybook(loadCheck(t, fmt.Sprintf(`{id: c, description: pb, type: ansible-playbook, playbook: %q}`, missing)))
	if r.Passed {
		t.Errorf("a missing playbook should fail, got %+v", r)
	}
	if r.Error != "" {
		t.Errorf("a missing playbook is a clean fail, not an inspection Error; got Error=%q", r.Error)
	}
	if !strings.Contains(r.Detail, "not found") {
		t.Errorf("Detail should say the playbook was not found, got %q", r.Detail)
	}
}

func TestAnsiblePlaybook_noRecapSurfacesError(t *testing.T) {
	pb := tempPlaybook(t)
	stubAnsible(t, func(_, _, _ string) (string, error) {
		return "", fmt.Errorf(`exec: "ansible-playbook": executable file not found in $PATH`)
	})
	r := checkAnsiblePlaybook(loadCheck(t, fmt.Sprintf(`{id: c, description: pb, type: ansible-playbook, playbook: %q}`, pb)))
	if r.Error == "" {
		t.Errorf("missing ansible-core (no output) should surface as an inspection Error, got %+v", r)
	}
}

func TestAnsiblePlaybook_idempotentFalseRunsOnce(t *testing.T) {
	pb := tempPlaybook(t)
	calls := 0
	stubAnsible(t, func(_, _, _ string) (string, error) {
		calls++
		return recapOut(5, 3, 0, 0), nil // changes, but idempotency is not required
	})
	r := checkAnsiblePlaybook(loadCheck(t, fmt.Sprintf(`{id: c, description: pb, type: ansible-playbook, playbook: %q, idempotent: false}`, pb)))
	if !r.Passed {
		t.Errorf("with idempotent:false a single clean run should pass despite changes, got %+v", r)
	}
	if calls != 1 {
		t.Errorf("idempotent:false should run the playbook once, ran %d", calls)
	}
}
