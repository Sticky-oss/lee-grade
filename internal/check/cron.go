package check

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/sticky-oss/lee-grade/internal/task"
)

func init() {
	Register("cron-job", CheckerFunc(checkCronJob))
}

// cronJobArgs is the inline argument schema for `type: cron-job`.
//
// It verifies that a scheduled job exists (or is absent). The source is
// either a user crontab (`crontab -l -u <user>`) or an explicit cron file
// such as /etc/crontab or a drop-in under /etc/cron.d.
//
// A job matches when ALL specified predicates hold on the same entry line:
//   - command  : the line contains this substring
//   - schedule  : the line's leading time fields equal this (e.g. "0 2 * * *"
//                 or a "@daily" shorthand)
//   - matches  : the whole line matches this regular expression
//
// Example YAML:
//
//	- id: nightly-backup
//	  description: rocky has a 2am cron job running /opt/backup.sh
//	  type: cron-job
//	  user: rocky
//	  schedule: "0 2 * * *"
//	  command: /opt/backup.sh
type cronJobArgs struct {
	// User selects whose crontab to read via `crontab -l -u <user>`. Empty
	// = the current user's crontab. Ignored when File is set.
	User string `yaml:"user,omitempty"`
	// File reads an explicit cron file (e.g. /etc/crontab,
	// /etc/cron.d/backup) instead of a user crontab. These files carry an
	// extra user field after the schedule; schedule matching still compares
	// the leading time fields.
	File string `yaml:"file,omitempty"`

	// Command is a substring the matching entry must contain.
	Command string `yaml:"command,omitempty"`
	// Schedule is the time spec: five fields ("min hour dom mon dow") or a
	// "@"-shorthand ("@daily", "@reboot"). Whitespace is normalized before
	// comparison so "0  2 * * *" and "0 2 * * *" are equal.
	Schedule string `yaml:"schedule,omitempty"`
	// Matches is a regular expression the whole entry line must match.
	Matches string `yaml:"matches,omitempty"`

	// Present defaults to true. Set false to assert NO entry matches.
	Present *bool `yaml:"present,omitempty"`
}

func checkCronJob(c *task.Check) Result {
	var args cronJobArgs
	if err := c.DecodeArgs(&args); err != nil {
		return Result{Error: err.Error()}
	}
	if args.Command == "" && args.Schedule == "" && args.Matches == "" {
		return Result{Error: "check 'cron-job' requires at least one of: command, schedule, matches"}
	}

	var re *regexp.Regexp
	if args.Matches != "" {
		var err error
		re, err = regexp.Compile(args.Matches)
		if err != nil {
			return Result{Error: fmt.Sprintf("check 'cron-job': invalid matches regexp %q: %v", args.Matches, err)}
		}
	}

	content, src, err := cronContent(args)
	if err != nil {
		return Result{Error: err.Error()}
	}

	matched := false
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if args.Command != "" && !strings.Contains(line, args.Command) {
			continue
		}
		if args.Schedule != "" && !cronScheduleMatches(line, args.Schedule) {
			continue
		}
		if re != nil && !re.MatchString(line) {
			continue
		}
		matched = true
		break
	}

	want := presentDefaultTrue(args.Present)
	if matched != want {
		if want {
			return Result{Passed: false, Detail: fmt.Sprintf("no matching cron entry in %s", src)}
		}
		return Result{Passed: false, Detail: fmt.Sprintf("an unwanted matching cron entry exists in %s", src)}
	}
	return Result{Passed: true}
}

// cronContent returns the crontab text to scan and a human label for it.
// A missing user crontab or missing cron file yields empty content (not an
// error) so absence assertions grade cleanly.
func cronContent(args cronJobArgs) (content, src string, err error) {
	if args.File != "" {
		b, readErr := os.ReadFile(args.File)
		if readErr != nil {
			if os.IsNotExist(readErr) {
				return "", args.File, nil
			}
			return "", args.File, fmt.Errorf("read %s: %w", args.File, readErr)
		}
		return string(b), args.File, nil
	}

	cmdArgs := []string{"-l"}
	label := "current user's crontab"
	if args.User != "" {
		cmdArgs = append(cmdArgs, "-u", args.User)
		label = fmt.Sprintf("%s's crontab", args.User)
	}
	out, runErr := runCmd("crontab", cmdArgs...)
	if runErr != nil {
		// `crontab -l` exits 1 with "no crontab for X" when the user simply
		// has no crontab — that's empty content, not a tool failure.
		if strings.Contains(out, "no crontab for") {
			return "", label, nil
		}
		return "", label, fmt.Errorf("crontab %s: %v (%s) — is cronie installed?",
			strings.Join(cmdArgs, " "), runErr, strings.TrimSpace(out))
	}
	return out, label, nil
}

// cronScheduleMatches reports whether a crontab line's leading time spec
// equals want. Handles both 5-field specs and "@"-shorthands, and tolerates
// arbitrary inter-field whitespace.
func cronScheduleMatches(line, want string) bool {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return false
	}
	want = strings.TrimSpace(want)
	if strings.HasPrefix(want, "@") {
		return fields[0] == want
	}
	wantFields := strings.Fields(want)
	if len(wantFields) != 5 || len(fields) < 5 {
		return false
	}
	for i := 0; i < 5; i++ {
		if fields[i] != wantFields[i] {
			return false
		}
	}
	return true
}
