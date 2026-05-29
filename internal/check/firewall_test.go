package check

import "testing"

// fakeExit returns a runCmd stub that produces a genuine exit code via real
// commands: 0 → `true`, 1 → `false`, anything else → a missing binary (start
// error, exitCode -1). This exercises checkFirewall's exit-status branches
// with real *exec.ExitError values instead of fabricated ones.
func fakeExit(code int) func(string, ...string) (string, error) {
	return func(name string, args ...string) (string, error) {
		switch code {
		case 0:
			return runReal("true")
		case 1:
			return runReal("false")
		default:
			return runReal("lee-grade-no-such-binary-xyz")
		}
	}
}

func TestFirewall_servicePresent(t *testing.T) {
	stubRunCmd(t, fakeExit(0))
	r := checkFirewall(loadCheck(t, `{id: c, description: http, type: firewall, service: http, permanent: true}`))
	if !r.Passed {
		t.Errorf("service present (exit 0) should pass, got %+v", r)
	}
}

func TestFirewall_serviceAbsentFailsWhenWanted(t *testing.T) {
	stubRunCmd(t, fakeExit(1))
	r := checkFirewall(loadCheck(t, `{id: c, description: http, type: firewall, service: http}`))
	if r.Passed {
		t.Errorf("service absent (exit 1) should fail when present is wanted, got %+v", r)
	}
	if r.Detail == "" {
		t.Errorf("expected Detail explaining absence")
	}
}

func TestFirewall_absentSatisfiesPresentFalse(t *testing.T) {
	stubRunCmd(t, fakeExit(1))
	r := checkFirewall(loadCheck(t, `{id: c, description: no-telnet, type: firewall, port: 23/tcp, present: false}`))
	if !r.Passed {
		t.Errorf("absent binding should satisfy present:false, got %+v", r)
	}
}

func TestFirewall_presentFailsPresentFalse(t *testing.T) {
	stubRunCmd(t, fakeExit(0))
	r := checkFirewall(loadCheck(t, `{id: c, description: no-telnet, type: firewall, port: 23/tcp, present: false}`))
	if r.Passed {
		t.Errorf("present binding should fail present:false, got %+v", r)
	}
}

func TestFirewall_masquerade(t *testing.T) {
	stubRunCmd(t, fakeExit(0))
	if r := checkFirewall(loadCheck(t, `{id: c, description: masq, type: firewall, masquerade: true, zone: public}`)); !r.Passed {
		t.Errorf("masquerade on (exit 0) with masquerade:true should pass, got %+v", r)
	}
	stubRunCmd(t, fakeExit(1))
	if r := checkFirewall(loadCheck(t, `{id: c, description: masq, type: firewall, masquerade: true, zone: public}`)); r.Passed {
		t.Errorf("masquerade off (exit 1) with masquerade:true should fail, got %+v", r)
	}
}

func TestFirewall_toolMissingErrors(t *testing.T) {
	stubRunCmd(t, fakeExit(-1))
	r := checkFirewall(loadCheck(t, `{id: c, description: http, type: firewall, service: http}`))
	if r.Error == "" {
		t.Errorf("missing firewall-cmd should surface as Error, not a clean fail; got %+v", r)
	}
}

func TestFirewall_validationErrors(t *testing.T) {
	if r := checkFirewall(loadCheck(t, `{id: c, description: empty, type: firewall}`)); r.Error == "" {
		t.Errorf("no binding should error")
	}
	if r := checkFirewall(loadCheck(t, `{id: c, description: two, type: firewall, service: http, port: 80/tcp}`)); r.Error == "" {
		t.Errorf("two bindings should error")
	}
}
