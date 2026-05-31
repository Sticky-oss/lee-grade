package check

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Remote check execution.
//
// A check may carry an optional `host` (a logical managed-node name). When set,
// a remote-capable checker runs its inspection ON that node over SSH instead of
// the local control host — the multi-node grading path for the RHCE topology.
//
// The transport reuses the existing runCmd seam: a remote run is just
//
//	runCmd("ssh", <sshArgs...>, "sudo -n <shell-quoted command>")
//
// so every command-based check becomes remote-capable without new plumbing, and
// the same tests that stub runCmd can assert the SSH command was built right.

// HostSpec describes how to reach one managed node over SSH. Populated by the
// CLI from the --hosts file and handed to SetHosts.
type HostSpec struct {
	Address string // hostname or IP
	User    string // SSH login user (default "root" if empty)
	Key     string // path to the private key (optional)
	Port    int    // SSH port (default 22)
}

// hosts is the logical-name → HostSpec map the runner resolves `host` against.
// nil until the CLI calls SetHosts; an empty/absent map makes remote checks
// fail with a clear "host not configured" message rather than grading
// localhost by accident.
var hosts map[string]HostSpec

// SetHosts installs the resolver map (called once by the CLI after parsing
// --hosts). A copy is taken so later mutation of the caller's map is harmless.
func SetHosts(h map[string]HostSpec) {
	hosts = make(map[string]HostSpec, len(h))
	for k, v := range h {
		hosts[k] = v
	}
}

// sshArgsFor builds the ssh option list + target for a logical host name.
func sshArgsFor(name string) ([]string, bool) {
	h, ok := hosts[name]
	if !ok {
		return nil, false
	}
	port := h.Port
	if port == 0 {
		port = 22
	}
	args := []string{
		"-q",
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=no",
		"-o", "ConnectTimeout=8",
		"-p", strconv.Itoa(port),
	}
	if h.Key != "" {
		args = append(args, "-i", h.Key)
	}
	user := h.User
	if user == "" {
		user = "root"
	}
	return append(args, user+"@"+h.Address), true
}

// runOn dispatches a command either locally (host == "") through the existing
// runCmd seam, or remotely over SSH. Remote commands run under `sudo -n` so
// privileged inspections (reading /etc, querying systemd) work with the
// unprivileged login user that has passwordless sudo on the node.
func runOn(host, name string, args ...string) (string, error) {
	if host == "" {
		return runCmd(name, args...)
	}
	ssh, ok := sshArgsFor(host)
	if !ok {
		return "", fmt.Errorf("check targets host %q, but it is not in the --hosts file", host)
	}
	return runCmd("ssh", append(ssh, remoteCmd(name, args...))...)
}

// remoteCmd builds the string ssh hands to the node's shell: run under
// passwordless sudo, with LC_ALL=C so coreutils messages (e.g. "No such file
// or directory", rpm "not installed") stay in the English form the checkers
// match on regardless of the node's locale.
func remoteCmd(name string, args ...string) string {
	return "sudo -n env LC_ALL=C " + shellJoin(append([]string{name}, args...))
}

// runCmdOut is like runCmd but captures stdout ONLY (not stderr). The command
// check uses it so a tool that writes a warning to stderr while exiting 0
// doesn't pollute an equals/matches assertion. A package var for the test seam.
var runCmdOut = func(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return string(out), err
}

// runOnStdout mirrors runOn but returns stdout only (via runCmdOut). For a
// remote run, ssh forwards the node's stdout to ssh's stdout, so capturing
// ssh's stdout yields the remote command's stdout.
func runOnStdout(host, name string, args ...string) (string, error) {
	if host == "" {
		return runCmdOut(name, args...)
	}
	ssh, ok := sshArgsFor(host)
	if !ok {
		return "", fmt.Errorf("check targets host %q, but it is not in the --hosts file", host)
	}
	return runCmdOut("ssh", append(ssh, remoteCmd(name, args...))...)
}

// shellJoin renders a command + args as a single POSIX-sh-safe string for the
// remote shell ssh hands the command to.
func shellJoin(parts []string) string {
	q := make([]string, len(parts))
	for i, p := range parts {
		q[i] = shellQuote(p)
	}
	return strings.Join(q, " ")
}

// shellQuote single-quotes a token, escaping embedded single quotes the
// classic '\'' way, so no character is special to the remote shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// remoteCapable is the set of check types whose inspection can run on a remote
// node. A check with `host` set whose type is NOT here is rejected by the
// runner — silently grading localhost would be a correctness bug.
var remoteCapable = map[string]bool{
	"service-state":     true,
	"package-installed": true,
	"file-content":      true,
	"file":              true,
	"user":              true,
	"group":             true,
	"user-in-group":     true,
	"mount":             true,
	"command":           true,
}
