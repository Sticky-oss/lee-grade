package check

import (
	"os/exec"
	"testing"
)

// stubRunCmd replaces the package command runner for the duration of a test
// and restores it via t.Cleanup. The selinux/firewall/cron checkers can't be
// exercised live on lee-dev (SELinux disabled, no firewalld/cronie), so
// canned output is the only way to test their logic. Do not run these tests
// in parallel — they mutate a shared package var.
func stubRunCmd(t *testing.T, fn func(name string, args ...string) (string, error)) {
	t.Helper()
	orig := runCmd
	runCmd = fn
	t.Cleanup(func() { runCmd = orig })
}

// runReal executes a command for real — used by exit-code tests that need a
// genuine *exec.ExitError (true → 0, false → 1, missing binary → start error).
func runReal(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}

func TestSELinux_modePassAndFail(t *testing.T) {
	stubRunCmd(t, func(name string, args ...string) (string, error) {
		return "Enforcing\n", nil
	})
	pass := checkSELinux(loadCheck(t, `{id: c, description: enforcing, type: selinux, mode: enforcing}`))
	if !pass.Passed {
		t.Errorf("mode enforcing should pass, got %+v", pass)
	}
	fail := checkSELinux(loadCheck(t, `{id: c, description: permissive, type: selinux, mode: permissive}`))
	if fail.Passed {
		t.Errorf("mode permissive should fail when host is Enforcing, got %+v", fail)
	}
	if fail.Detail == "" {
		t.Errorf("expected Detail explaining the mismatch")
	}
}

func TestSELinux_booleanState(t *testing.T) {
	stubRunCmd(t, func(name string, args ...string) (string, error) {
		return "httpd_can_network_connect --> on\n", nil
	})
	if r := checkSELinux(loadCheck(t, `{id: c, description: on, type: selinux, boolean: httpd_can_network_connect, state: "on"}`)); !r.Passed {
		t.Errorf("boolean on should pass, got %+v", r)
	}
	if r := checkSELinux(loadCheck(t, `{id: c, description: off, type: selinux, boolean: httpd_can_network_connect, state: "off"}`)); r.Passed {
		t.Errorf("boolean off should fail when host has it on, got %+v", r)
	}
}

func TestSELinux_fileContextType(t *testing.T) {
	stubRunCmd(t, func(name string, args ...string) (string, error) {
		return "system_u:object_r:httpd_sys_content_t:s0\n", nil
	})
	if r := checkSELinux(loadCheck(t, `{id: c, description: type, type: selinux, path: /srv/web, setype: httpd_sys_content_t}`)); !r.Passed {
		t.Errorf("matching setype should pass, got %+v", r)
	}
	if r := checkSELinux(loadCheck(t, `{id: c, description: wrong, type: selinux, path: /srv/web, setype: default_t}`)); r.Passed {
		t.Errorf("wrong setype should fail, got %+v", r)
	}
	if r := checkSELinux(loadCheck(t, `{id: c, description: full, type: selinux, path: /srv/web, context: "system_u:object_r:httpd_sys_content_t:s0"}`)); !r.Passed {
		t.Errorf("exact full context should pass, got %+v", r)
	}
}

func TestSELinux_portLabel(t *testing.T) {
	sample := "http_port_t                    tcp      80, 81, 443, 488, 8008, 8009, 8443, 9000\n" +
		"ssh_port_t                     tcp      22\n" +
		"vnc_port_t                     tcp      5900-5999, 6000-6020\n"
	stubRunCmd(t, func(name string, args ...string) (string, error) { return sample, nil })

	if r := checkSELinux(loadCheck(t, `{id: c, description: p80, type: selinux, port: 80, proto: tcp, setype: http_port_t}`)); !r.Passed {
		t.Errorf("80/tcp http_port_t should pass, got %+v", r)
	}
	if r := checkSELinux(loadCheck(t, `{id: c, description: range, type: selinux, port: 6010, proto: tcp, setype: vnc_port_t}`)); !r.Passed {
		t.Errorf("6010/tcp inside vnc range should pass, got %+v", r)
	}
	if r := checkSELinux(loadCheck(t, `{id: c, description: nope, type: selinux, port: 9999, proto: tcp, setype: http_port_t}`)); r.Passed {
		t.Errorf("9999/tcp should not be labeled http_port_t, got %+v", r)
	}
}

func TestSELinux_validationErrors(t *testing.T) {
	if r := checkSELinux(loadCheck(t, `{id: c, description: empty, type: selinux}`)); r.Error == "" {
		t.Errorf("no aspect should error")
	}
	if r := checkSELinux(loadCheck(t, `{id: c, description: two, type: selinux, mode: enforcing, boolean: x, state: "on"}`)); r.Error == "" {
		t.Errorf("two aspects should error")
	}
}

func TestSELinux_modeLive(t *testing.T) {
	// Uses the real getenforce on the host. On lee-dev this reports
	// "Disabled"; the test asserts our checker agrees with reality rather
	// than hard-coding a mode (so it also passes on enforcing hosts).
	real, err := runReal("getenforce")
	if err != nil {
		t.Skipf("getenforce unavailable: %v", err)
	}
	mode := trimLower(real)
	r := checkSELinux(loadCheck(t, `{id: c, description: live, type: selinux, mode: `+mode+`}`))
	if !r.Passed {
		t.Errorf("live mode %q should pass against real getenforce, got %+v", mode, r)
	}
}

func TestParseGetsebool(t *testing.T) {
	cases := map[string]struct {
		want string
		ok   bool
	}{
		"foo --> on\n":            {"on", true},
		"foo --> off":             {"off", true},
		"foo_bar_baz -->  on  \n": {"on", true},
		"garbage":                 {"", false},
	}
	for in, exp := range cases {
		got, ok := parseGetsebool(in)
		if got != exp.want || ok != exp.ok {
			t.Errorf("parseGetsebool(%q) = (%q,%v), want (%q,%v)", in, got, ok, exp.want, exp.ok)
		}
	}
}

func TestContextType(t *testing.T) {
	if got := contextType("system_u:object_r:httpd_sys_content_t:s0"); got != "httpd_sys_content_t" {
		t.Errorf("contextType = %q", got)
	}
	if got := contextType("weird"); got != "weird" {
		t.Errorf("contextType fallback = %q", got)
	}
}

func TestNormalizeOnOff(t *testing.T) {
	for _, in := range []string{"on", "true", "YES", "1", "enabled"} {
		if v, ok := normalizeOnOff(in); !ok || v != "on" {
			t.Errorf("normalizeOnOff(%q) = (%q,%v)", in, v, ok)
		}
	}
	for _, in := range []string{"off", "false", "No", "0", "disabled"} {
		if v, ok := normalizeOnOff(in); !ok || v != "off" {
			t.Errorf("normalizeOnOff(%q) = (%q,%v)", in, v, ok)
		}
	}
	if _, ok := normalizeOnOff("maybe"); ok {
		t.Errorf("normalizeOnOff(maybe) should be !ok")
	}
}

func trimLower(s string) string {
	out := ""
	for _, r := range s {
		if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
			continue
		}
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		out += string(r)
	}
	return out
}
