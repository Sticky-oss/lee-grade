package check

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/sticky-oss/lee-grade/internal/task"
)

func init() {
	Register("service-state", CheckerFunc(checkServiceState))
	Register("package-installed", CheckerFunc(checkPackageInstalled))
}

// serviceStateArgs is the inline argument schema for `type: service-state`.
//
// Each assertion is optional — missing == "don't care". Combine to assert
// multiple aspects of one unit.
//
// Example YAML:
//
//	- id: chronyd-enabled-and-active
//	  description: chronyd is enabled at boot and running now
//	  type: service-state
//	  unit: chronyd
//	  enabled: true
//	  active: true
type serviceStateArgs struct {
	// Unit is the systemd unit name. The `.service` suffix is optional;
	// systemctl accepts both.
	Unit string `yaml:"unit"`
	// Active asserts `systemctl is-active <unit>` equals "active" (true)
	// or anything else (false).
	Active *bool `yaml:"active,omitempty"`
	// Enabled asserts `systemctl is-enabled <unit>` equals "enabled" (true)
	// or anything else (false).
	Enabled *bool `yaml:"enabled,omitempty"`
	// Masked asserts the unit is masked (true) or not (false).
	Masked *bool `yaml:"masked,omitempty"`
}

func checkServiceState(c *task.Check) Result {
	var args serviceStateArgs
	if err := c.DecodeArgs(&args); err != nil {
		return Result{Error: err.Error()}
	}
	if args.Unit == "" {
		return Result{Error: "check 'service-state' requires field 'unit'"}
	}
	if args.Active == nil && args.Enabled == nil && args.Masked == nil {
		return Result{Error: "check 'service-state' requires at least one of: active, enabled, masked"}
	}

	if args.Active != nil {
		out, err := runOn(c.Host, "systemctl", "is-active", args.Unit)
		state := strings.TrimSpace(out)
		if !isActiveWord(state) {
			// systemctl didn't answer with a recognized state — that's an
			// inspection failure (ssh down, sudo denied, binary missing), not
			// a "service is inactive" assertion result.
			return Result{Error: fmt.Sprintf("could not query is-active for %s: %v (%s)", args.Unit, err, oneLine(out))}
		}
		if (state == "active") != *args.Active {
			return Result{Passed: false, Detail: fmt.Sprintf(
				"unit %s is-active = %q, want %s",
				args.Unit, state, ifThen(*args.Active, "active", "inactive"),
			)}
		}
	}
	if args.Enabled != nil {
		out, err := runOn(c.Host, "systemctl", "is-enabled", args.Unit)
		state := strings.TrimSpace(out)
		if !isEnabledWord(state) {
			return Result{Error: fmt.Sprintf("could not query is-enabled for %s: %v (%s)", args.Unit, err, oneLine(out))}
		}
		isEnabled := state == "enabled" || state == "enabled-runtime" || state == "alias"
		if isEnabled != *args.Enabled {
			return Result{Passed: false, Detail: fmt.Sprintf(
				"unit %s is-enabled = %q, want %s",
				args.Unit, state, ifThen(*args.Enabled, "enabled", "not enabled"),
			)}
		}
	}
	if args.Masked != nil {
		out, err := runOn(c.Host, "systemctl", "is-enabled", args.Unit)
		state := strings.TrimSpace(out)
		if !isEnabledWord(state) {
			return Result{Error: fmt.Sprintf("could not query is-enabled for %s: %v (%s)", args.Unit, err, oneLine(out))}
		}
		isMasked := strings.HasPrefix(state, "masked") // masked or masked-runtime
		if isMasked != *args.Masked {
			return Result{Passed: false, Detail: fmt.Sprintf(
				"unit %s masked=%v, want masked=%v",
				args.Unit, isMasked, *args.Masked,
			)}
		}
	}
	return Result{Passed: true}
}

// isActiveWord reports whether s is a value `systemctl is-active` actually
// emits, used to tell "systemctl answered" apart from a transport/tool failure.
func isActiveWord(s string) bool {
	switch s {
	case "active", "inactive", "activating", "deactivating", "reloading", "failed", "unknown", "maintenance":
		return true
	}
	return false
}

// isEnabledWord reports whether s is a value `systemctl is-enabled` emits.
func isEnabledWord(s string) bool {
	switch s {
	case "enabled", "enabled-runtime", "disabled", "static", "indirect",
		"masked", "masked-runtime", "alias", "generated", "transient", "bad", "not-found":
		return true
	}
	return false
}

// packageInstalledArgs is the inline argument schema for `type: package-installed`.
type packageInstalledArgs struct {
	Name string `yaml:"name"`
	// Version is optional. When set, the installed version is compared as
	// an exact-prefix match (so "1.4" matches "1.4.0-1.el9").
	Version string `yaml:"version,omitempty"`
}

func checkPackageInstalled(c *task.Check) Result {
	var args packageInstalledArgs
	if err := c.DecodeArgs(&args); err != nil {
		return Result{Error: err.Error()}
	}
	if args.Name == "" {
		return Result{Error: "check 'package-installed' requires field 'name'"}
	}

	out, err := runOn(c.Host, "rpm", "-q", "--queryformat", "%{NAME} %{VERSION}-%{RELEASE}\n", args.Name)
	if err != nil {
		// rpm exits 1 for "not installed" but also "command not found" if
		// rpm itself is absent. Distinguish via the stderr text.
		if strings.Contains(out, "not installed") {
			return Result{Passed: false, Detail: fmt.Sprintf("package %s is not installed", args.Name)}
		}
		// Fall through to a dpkg attempt for Debian-derived systems —
		// useful for cross-distro CI even though the primary target is
		// RHEL/Rocky.
		if dpkgOut, dpkgErr := runOn(c.Host, "dpkg-query", "-W", "-f=${binary:Package} ${Version}\n", args.Name); dpkgErr == nil {
			if args.Version != "" && !strings.Contains(dpkgOut, args.Version) {
				return Result{Passed: false, Detail: fmt.Sprintf(
					"package %s installed but version %q not present", args.Name, args.Version,
				)}
			}
			return Result{Passed: true}
		}
		return Result{Passed: false, Error: fmt.Sprintf("rpm -q %s: %v", args.Name, err)}
	}
	if args.Version != "" {
		fields := strings.Fields(out)
		if len(fields) < 2 {
			return Result{Error: fmt.Sprintf("rpm -q %s: unexpected output %q", args.Name, oneLine(out))}
		}
		if !strings.HasPrefix(fields[1], args.Version) {
			return Result{Passed: false, Detail: fmt.Sprintf(
				"package %s installed but version %q not present (got %q)",
				args.Name, args.Version, fields[1],
			)}
		}
	}
	return Result{Passed: true}
}

// runCmd is a small wrapper that returns combined stdout/stderr + error.
// Using CombinedOutput so error paths surface real shell wording in Detail.
//
// It is a package var, not a plain func, so tests can substitute canned
// tool output. lee-dev (WSL Rocky) has SELinux disabled and no
// firewalld/cronie, so the selinux/firewall/cron checkers can only be
// exercised with injected output; overriding runCmd is that seam.
var runCmd = func(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}

// exitCode extracts the process exit code from an error returned by runCmd.
// Returns 0 when err is nil, the real code for an *exec.ExitError, and -1
// when the command could not be started (binary missing). Used by checkers
// like firewall-cmd whose --query-* subcommands signal yes/no purely via
// exit status.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

func ifThen(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}
