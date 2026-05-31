package check

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/sticky-oss/lee-grade/internal/task"
)

func init() {
	Register("command", CheckerFunc(checkCommand))
}

// commandArgs is the inline schema for `type: command` — run a shell command
// and assert on its exit code and/or its output. The escape hatch for EX200/
// EX294 objectives without a dedicated check type (default boot target, tuned
// profile, active swap, ACLs, timezone, LVM, …). Remote-capable: set host: to
// run it on a managed node.
//
// The command is run via `sh -c`, so pipes and redirection work. Output is the
// combined stdout+stderr, trimmed, for the equals/contains/matches assertions.
//
// Example:
//
//	- id: default-target
//	  description: the default boot target is multi-user.target
//	  type: command
//	  run: systemctl get-default
//	  equals: multi-user.target
type commandArgs struct {
	Run string `yaml:"run"`
	// Exit is the expected exit code; defaults to 0.
	Exit *int `yaml:"exit,omitempty"`
	// At most ONE output assertion may be set.
	Equals   string `yaml:"equals,omitempty"`
	Contains string `yaml:"contains,omitempty"`
	Matches  string `yaml:"matches,omitempty"`
}

func checkCommand(c *task.Check) Result {
	var args commandArgs
	if err := c.DecodeArgs(&args); err != nil {
		return Result{Error: err.Error()}
	}
	if strings.TrimSpace(args.Run) == "" {
		return Result{Error: "check 'command' requires field 'run'"}
	}
	set := 0
	for _, on := range []bool{args.Equals != "", args.Contains != "", args.Matches != ""} {
		if on {
			set++
		}
	}
	if set > 1 {
		return Result{Error: "check 'command' takes at most one of: equals, contains, matches"}
	}

	out, err := runOn(c.Host, "sh", "-c", args.Run)
	code := exitCode(err)
	wantExit := 0
	if args.Exit != nil {
		wantExit = *args.Exit
	}
	if code != wantExit {
		return Result{Passed: false, Detail: fmt.Sprintf("`%s` exited %d, want %d (output: %s)", args.Run, code, wantExit, oneLine(out))}
	}

	got := strings.TrimSpace(out)
	switch {
	case args.Equals != "":
		if got != args.Equals {
			return Result{Passed: false, Detail: fmt.Sprintf("`%s` output %q, want %q", args.Run, oneLine(out), args.Equals)}
		}
	case args.Contains != "":
		if !strings.Contains(out, args.Contains) {
			return Result{Passed: false, Detail: fmt.Sprintf("`%s` output does not contain %q (got: %s)", args.Run, args.Contains, oneLine(out))}
		}
	case args.Matches != "":
		re, rerr := regexp.Compile(args.Matches)
		if rerr != nil {
			return Result{Error: fmt.Sprintf("check 'command': invalid matches regexp %q: %v", args.Matches, rerr)}
		}
		if !re.MatchString(out) {
			return Result{Passed: false, Detail: fmt.Sprintf("`%s` output does not match /%s/ (got: %s)", args.Run, args.Matches, oneLine(out))}
		}
	}
	return Result{Passed: true}
}

// oneLine collapses command output to a single tidy line for Detail messages.
func oneLine(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if s == "" {
		return "(no output)"
	}
	if len(s) > 120 {
		return s[:117] + "..."
	}
	return s
}
