package check

import (
	"errors"
	"strings"
	"testing"

	"github.com/sticky-oss/lee-grade/internal/task"
)

func withHosts(t *testing.T, h map[string]HostSpec) {
	t.Helper()
	SetHosts(h)
	t.Cleanup(func() { hosts = nil })
}

func TestShellQuote(t *testing.T) {
	cases := []struct{ in, want string }{
		{"abc", "'abc'"},
		{"a b", "'a b'"},
		{"it's", `'it'\''s'`},
	}
	for _, tc := range cases {
		if got := shellQuote(tc.in); got != tc.want {
			t.Errorf("shellQuote(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRunOn_localUsesRunCmdDirectly(t *testing.T) {
	var gotName string
	var gotArgs []string
	stubRunCmd(t, func(name string, args ...string) (string, error) {
		gotName, gotArgs = name, args
		return "ok", nil
	})
	if _, err := runOn("", "systemctl", "is-active", "httpd"); err != nil {
		t.Fatal(err)
	}
	if gotName != "systemctl" {
		t.Errorf("local runOn should call runCmd(systemctl,...), got %q", gotName)
	}
	if strings.Join(gotArgs, " ") != "is-active httpd" {
		t.Errorf("local args = %v", gotArgs)
	}
}

func TestRunOn_remoteUnknownHostErrors(t *testing.T) {
	withHosts(t, map[string]HostSpec{})
	if _, err := runOn("ghost", "true"); err == nil {
		t.Error("expected an error for an unconfigured host")
	}
}

func TestServiceState_remoteBuildsSSHCommand(t *testing.T) {
	withHosts(t, map[string]HostSpec{"node2": {Address: "10.0.0.5", User: "ansible", Key: "/k"}})
	var gotName string
	var gotArgs []string
	stubRunCmd(t, func(name string, args ...string) (string, error) {
		gotName, gotArgs = name, args
		return "active\n", nil
	})
	r := checkServiceState(loadCheck(t, `{id: c, description: d, type: service-state, host: node2, unit: httpd, active: true}`))
	if !r.Passed {
		t.Fatalf("expected pass, got %+v", r)
	}
	if gotName != "ssh" {
		t.Fatalf("a remote check should invoke ssh, got %q", gotName)
	}
	joined := strings.Join(gotArgs, " ")
	if !strings.Contains(joined, "ansible@10.0.0.5") {
		t.Errorf("ssh target missing in %v", gotArgs)
	}
	if !strings.Contains(joined, "-i /k") {
		t.Errorf("ssh key missing in %v", gotArgs)
	}
	last := gotArgs[len(gotArgs)-1]
	if last != "sudo -n env LC_ALL=C 'systemctl' 'is-active' 'httpd'" {
		t.Errorf("remote command = %q", last)
	}
}

func TestServiceState_transportErrorIsError(t *testing.T) {
	withHosts(t, map[string]HostSpec{"node2": {Address: "10.0.0.5"}})
	stubRunCmd(t, func(name string, args ...string) (string, error) {
		return "", errors.New("exit status 255") // ssh failed: no status word
	})
	r := checkServiceState(loadCheck(t, `{id: c, description: d, type: service-state, host: node2, unit: httpd, active: true}`))
	if r.Passed || r.Error == "" {
		t.Errorf("a transport failure must be an Error, not a clean pass/fail; got %+v", r)
	}
}

func TestFileContent_remoteReadsViaCat(t *testing.T) {
	withHosts(t, map[string]HostSpec{"node1": {Address: "10.0.0.4", User: "ansible", Key: "/k"}})
	var lastArg string
	stubRunCmd(t, func(name string, args ...string) (string, error) {
		if len(args) > 0 {
			lastArg = args[len(args)-1]
		}
		return "node = node1\nsite = LEE practice lab\n", nil
	})
	r := checkFileContent(loadCheck(t, `{id: c, description: d, type: file-content, host: node1, path: /etc/lee-fleet/node.conf, contains: "LEE practice lab"}`))
	if !r.Passed {
		t.Fatalf("expected contains-pass, got %+v", r)
	}
	if !strings.Contains(lastArg, "cat") || !strings.Contains(lastArg, "/etc/lee-fleet/node.conf") {
		t.Errorf("remote read command = %q", lastArg)
	}
}

func TestFileContent_remoteMissingFile(t *testing.T) {
	withHosts(t, map[string]HostSpec{"node1": {Address: "10.0.0.4"}})
	stubRunCmd(t, func(name string, args ...string) (string, error) {
		return "cat: /nope: No such file or directory\n", errors.New("exit status 1")
	})
	r := checkFileContent(loadCheck(t, `{id: c, description: d, type: file-content, host: node1, path: /nope, contains: x}`))
	if r.Passed {
		t.Error("a missing remote file must not pass")
	}
	if !strings.Contains(r.Detail, "does not exist") {
		t.Errorf("expected does-not-exist detail, got %+v", r)
	}
}

func TestRunTask_hostOnUnsupportedTypeErrors(t *testing.T) {
	withHosts(t, map[string]HostSpec{"node1": {Address: "10.0.0.4"}})
	// selinux is deliberately NOT remote-capable, so a host: on it must be
	// rejected by the runner rather than dispatched.
	tr := RunTask(&task.Task{
		ID: "t", Title: "x", Checks: []task.Check{
			{ID: "s", Description: "d", Type: "selinux", Host: "node1", Args: map[string]any{"mode": "enforcing"}},
		},
	})
	if tr.Checks[0].Passed {
		t.Error("a host on a non-remote-capable check must not pass")
	}
	if !strings.Contains(tr.Checks[0].Error, "does not support a remote") {
		t.Errorf("expected remote-unsupported error, got %+v", tr.Checks[0])
	}
}

func TestFile_remoteModeOwnerKindPass(t *testing.T) {
	withHosts(t, map[string]HostSpec{"node1": {Address: "10.0.0.4"}})
	stubRunCmd(t, func(name string, args ...string) (string, error) {
		return "644|root|root|regular file\n", nil // %a|%U|%G|%F
	})
	r := checkFile(loadCheck(t, `{id: c, description: d, type: file, host: node1, path: /etc/x, mode: 0644, owner: root, kind: file}`))
	if !r.Passed {
		t.Fatalf("expected pass, got %+v", r)
	}
}

func TestFile_remoteModeMismatch(t *testing.T) {
	withHosts(t, map[string]HostSpec{"node1": {Address: "10.0.0.4"}})
	stubRunCmd(t, func(name string, args ...string) (string, error) {
		return "600|root|root|regular file\n", nil
	})
	r := checkFile(loadCheck(t, `{id: c, description: d, type: file, host: node1, path: /etc/x, mode: 0644}`))
	if r.Passed {
		t.Error("mode 0600 should fail when 0644 is wanted")
	}
	if !strings.Contains(r.Detail, "0600") {
		t.Errorf("detail = %q", r.Detail)
	}
}

func TestUser_remotePass(t *testing.T) {
	withHosts(t, map[string]HostSpec{"node1": {Address: "10.0.0.4"}})
	stubRunCmd(t, func(name string, args ...string) (string, error) {
		return "leeops:x:1001:1001::/home/leeops:/bin/bash\n", nil
	})
	r := checkUser(loadCheck(t, `{id: c, description: d, type: user, host: node1, name: leeops, uid: 1001, shell: /bin/bash}`))
	if !r.Passed {
		t.Fatalf("expected pass, got %+v", r)
	}
}

func TestUser_remoteMissing(t *testing.T) {
	withHosts(t, map[string]HostSpec{"node1": {Address: "10.0.0.4"}})
	stubRunCmd(t, func(name string, args ...string) (string, error) {
		return "", errors.New("exit status 2")
	})
	r := checkUser(loadCheck(t, `{id: c, description: d, type: user, host: node1, name: ghost}`))
	if r.Passed || !strings.Contains(r.Detail, "does not exist") {
		t.Errorf("missing remote user should fail with does-not-exist, got %+v", r)
	}
}

func TestMount_remotePass(t *testing.T) {
	withHosts(t, map[string]HostSpec{"node1": {Address: "10.0.0.4"}})
	stubRunCmd(t, func(name string, args ...string) (string, error) {
		return "/dev/sdb1 /data xfs rw,noatime 0 0\nproc /proc proc rw 0 0\n", nil
	})
	r := checkMount(loadCheck(t, `{id: c, description: d, type: mount, host: node1, mountpoint: /data, fstype: xfs, contains_options: [noatime]}`))
	if !r.Passed {
		t.Fatalf("expected pass, got %+v", r)
	}
}
