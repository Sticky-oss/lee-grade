package check

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/sticky-oss/lee-grade/internal/task"
)

func init() {
	Register("ansible-playbook", CheckerFunc(checkAnsiblePlaybook))
}

// ansiblePlaybookArgs is the inline argument schema for `type: ansible-playbook`.
//
// This is the signature RHCE (EX294) rubric: a learner's playbook must both
// converge the managed host to the desired state AND be idempotent — a second
// run changes nothing. The grader runs the playbook itself and inspects the
// PLAY RECAP line(s).
//
// Note the division of labour: this check verifies the *playbook* (does it run
// clean and idempotently); the *resulting host state* is graded by the ordinary
// check types (package-installed, service-state, file-content, firewall, ...)
// in the same task. One task therefore both runs Ansible and asserts what it
// produced.
//
// Example YAML:
//
//	- id: web-playbook-idempotent
//	  description: web.yml converges and is idempotent (2nd run = no changes)
//	  type: ansible-playbook
//	  dir: /home/lee/ansible
//	  playbook: web.yml
//	  inventory: inventory
type ansiblePlaybookArgs struct {
	// Playbook is the playbook path (required). When Dir is set and this is
	// relative, it is resolved against Dir.
	Playbook string `yaml:"playbook"`
	// Inventory is an optional `-i` value (a path or a comma list). When empty,
	// Ansible falls back to a project ansible.cfg or its default inventory.
	Inventory string `yaml:"inventory,omitempty"`
	// Dir is an optional working directory to run from, so a project-local
	// ansible.cfg and an adjacent roles/ directory resolve exactly as they do
	// for the learner running the playbook by hand.
	Dir string `yaml:"dir,omitempty"`
	// Idempotent defaults to true: the grader runs the playbook twice and
	// requires the second run to report changed=0. Set false to require only a
	// single clean run (failed=0, unreachable=0) without the idempotency bar.
	Idempotent *bool `yaml:"idempotent,omitempty"`
}

func checkAnsiblePlaybook(c *task.Check) Result {
	var args ansiblePlaybookArgs
	if err := c.DecodeArgs(&args); err != nil {
		return Result{Error: err.Error()}
	}
	if args.Playbook == "" {
		return Result{Error: "check 'ansible-playbook' requires field 'playbook'"}
	}

	// A missing playbook is a clean failure (the learner hasn't written it
	// yet), not a system-inspection error.
	pbPath := args.Playbook
	if args.Dir != "" && !filepath.IsAbs(pbPath) {
		pbPath = filepath.Join(args.Dir, pbPath)
	}
	if _, err := os.Stat(pbPath); err != nil {
		if os.IsNotExist(err) {
			return Result{Passed: false, Detail: fmt.Sprintf("playbook %s not found", pbPath)}
		}
		return Result{Error: fmt.Sprintf("stat %s: %v", pbPath, err)}
	}

	idempotent := args.Idempotent == nil || *args.Idempotent
	runs := 1
	if idempotent {
		runs = 2
	}

	var last recap
	for i := 0; i < runs; i++ {
		out, runErr := runAnsiblePlaybook(args.Dir, args.Inventory, args.Playbook)
		rec, ok := parseRecap(out)
		if !ok {
			// No PLAY RECAP. Two very different causes: ansible-core isn't
			// installed (a genuine inspection failure → Error), or Ansible ran
			// and rejected the playbook, e.g. a syntax error (a clean fail).
			if strings.TrimSpace(out) == "" {
				return Result{Error: fmt.Sprintf(
					"ansible-playbook produced no output (%v) — is ansible-core installed?", runErr,
				)}
			}
			return Result{Passed: false, Detail: fmt.Sprintf(
				"playbook did not reach a PLAY RECAP (run %d): %s", i+1, recapTail(out),
			)}
		}
		if rec.Failed > 0 || rec.Unreachable > 0 {
			return Result{Passed: false, Detail: fmt.Sprintf(
				"playbook run %d had failed=%d unreachable=%d (want 0)", i+1, rec.Failed, rec.Unreachable,
			)}
		}
		last = rec
	}

	if idempotent && last.Changed != 0 {
		return Result{Passed: false, Detail: fmt.Sprintf(
			"playbook is not idempotent: the second run still reported changed=%d (want 0)", last.Changed,
		)}
	}
	return Result{Passed: true}
}

// recap is the aggregate of an Ansible PLAY RECAP, summed across every host
// line so a multi-node play is judged as a whole.
type recap struct {
	Ok, Changed, Unreachable, Failed int
}

// recapLineRe matches the four mandatory counters that appear, in this order,
// on every per-host PLAY RECAP line — regardless of host name or the trailing
// skipped/rescued/ignored fields.
var recapLineRe = regexp.MustCompile(`ok=(\d+)\s+changed=(\d+)\s+unreachable=(\d+)\s+failed=(\d+)`)

// parseRecap sums the per-host counters in ansible-playbook output. It returns
// ok=false when no recap line is present at all — meaning the run never got far
// enough to produce one (ansible-core missing, or the play aborted early).
func parseRecap(out string) (recap, bool) {
	ms := recapLineRe.FindAllStringSubmatch(out, -1)
	if len(ms) == 0 {
		return recap{}, false
	}
	var r recap
	for _, m := range ms {
		r.Ok += atoiSafe(m[1])
		r.Changed += atoiSafe(m[2])
		r.Unreachable += atoiSafe(m[3])
		r.Failed += atoiSafe(m[4])
	}
	return r, true
}

func atoiSafe(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// recapTail returns the last few non-empty output lines, joined for a single
// Detail line — enough to show the learner why a broken playbook never ran.
func recapTail(out string) string {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	var kept []string
	for i := len(lines) - 1; i >= 0 && len(kept) < 3; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" {
			kept = append([]string{s}, kept...)
		}
	}
	if len(kept) == 0 {
		return "(no output)"
	}
	return strings.Join(kept, " | ")
}

// runAnsiblePlaybook invokes ansible-playbook and returns combined output. It
// is a package var so tests can inject canned PLAY RECAP text without
// ansible-core installed (lee-dev/WSL has no Ansible; only the VM does — the
// same test seam as runCmd for the other check types).
var runAnsiblePlaybook = func(dir, inventory, playbook string) (string, error) {
	cmdArgs := make([]string, 0, 3)
	if inventory != "" {
		cmdArgs = append(cmdArgs, "-i", inventory)
	}
	cmdArgs = append(cmdArgs, playbook)
	cmd := exec.Command("ansible-playbook", cmdArgs...)
	if dir != "" {
		cmd.Dir = dir
	}
	// Force predictable, colour-free, default-callback output so parseRecap is
	// robust no matter what the learner's ansible.cfg sets.
	cmd.Env = append(os.Environ(),
		"ANSIBLE_NOCOLOR=1",
		"ANSIBLE_FORCE_COLOR=0",
		"ANSIBLE_STDOUT_CALLBACK=default",
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
