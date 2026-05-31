package check

import (
	"errors"
	"strings"
	"testing"
)

func TestCommand_equalsPass(t *testing.T) {
	stubRunCmd(t, func(name string, args ...string) (string, error) {
		return "multi-user.target\n", nil
	})
	r := checkCommand(loadCheck(t, `{id: c, description: d, type: command, run: systemctl get-default, equals: multi-user.target}`))
	if !r.Passed {
		t.Fatalf("expected pass, got %+v", r)
	}
}

func TestCommand_equalsFail(t *testing.T) {
	stubRunCmd(t, func(name string, args ...string) (string, error) {
		return "graphical.target\n", nil
	})
	r := checkCommand(loadCheck(t, `{id: c, description: d, type: command, run: systemctl get-default, equals: multi-user.target}`))
	if r.Passed || !strings.Contains(r.Detail, "graphical.target") {
		t.Errorf("expected fail mentioning actual output, got %+v", r)
	}
}

func TestCommand_containsAndExit(t *testing.T) {
	stubRunCmd(t, func(name string, args ...string) (string, error) {
		return "NAME      TYPE  SIZE\n/swapfile file  512M\n", nil
	})
	r := checkCommand(loadCheck(t, `{id: c, description: d, type: command, run: "swapon --show", contains: /swapfile}`))
	if !r.Passed {
		t.Fatalf("expected pass, got %+v", r)
	}
}

func TestCommand_exitMismatch(t *testing.T) {
	stubRunCmd(t, func(name string, args ...string) (string, error) {
		return "", errors.New("exit status 1")
	})
	r := checkCommand(loadCheck(t, `{id: c, description: d, type: command, run: "test -f /nope"}`))
	if r.Passed || !strings.Contains(r.Detail, "want 0") {
		t.Errorf("expected exit-mismatch fail, got %+v", r)
	}
}

func TestCommand_explicitNonZeroExit(t *testing.T) {
	// runReal produces a genuine *exec.ExitError so exitCode extracts the code.
	stubRunCmd(t, runReal)
	r := checkCommand(loadCheck(t, `{id: c, description: d, type: command, run: "exit 1", exit: 1}`))
	if !r.Passed {
		t.Fatalf("expected pass for matching exit code, got %+v", r)
	}
}

func TestCommand_remoteRunsViaSSHShell(t *testing.T) {
	withHosts(t, map[string]HostSpec{"node1": {Address: "10.0.0.4", User: "ansible"}})
	var last string
	stubRunCmd(t, func(name string, args ...string) (string, error) {
		if name != "ssh" {
			t.Errorf("remote command should run via ssh, got %q", name)
		}
		if len(args) > 0 {
			last = args[len(args)-1]
		}
		return "virtual-guest\n", nil
	})
	r := checkCommand(loadCheck(t, `{id: c, description: d, type: command, host: node1, run: "tuned-adm active", contains: virtual-guest}`))
	if !r.Passed {
		t.Fatalf("expected pass, got %+v", r)
	}
	if !strings.Contains(last, "sudo -n") || !strings.Contains(last, "tuned-adm active") {
		t.Errorf("remote command = %q", last)
	}
}
