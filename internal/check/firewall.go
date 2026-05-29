package check

import (
	"fmt"
	"strings"

	"github.com/sticky-oss/lee-grade/internal/task"
)

func init() {
	Register("firewall", CheckerFunc(checkFirewall))
}

// firewallArgs is the inline argument schema for `type: firewall`.
//
// One firewall check verifies exactly ONE binding, selected by which field
// is set: service, port, rich_rule, or masquerade. Each maps to a
// `firewall-cmd --query-*` subcommand, which signals presence purely via
// exit status (0 = present, 1 = absent).
//
// RHCSA grading almost always wants the PERMANENT config (survives reload),
// so set `permanent: true` to query `--permanent`. Default queries the
// running runtime config.
//
// Example YAML:
//
//	- id: http-permanent
//	  description: http service is permanently allowed in the default zone
//	  type: firewall
//	  service: http
//	  permanent: true
type firewallArgs struct {
	// Zone is the firewalld zone. Empty = the default zone.
	Zone string `yaml:"zone,omitempty"`

	// Service is a firewalld service name (http, https, nfs, …).
	Service string `yaml:"service,omitempty"`
	// Port is a port binding in firewalld's "port/proto" form, e.g. "8080/tcp".
	Port string `yaml:"port,omitempty"`
	// RichRule is a full rich-rule string, exactly as passed to
	// `firewall-cmd --add-rich-rule=`.
	RichRule string `yaml:"rich_rule,omitempty"`
	// Masquerade asserts masquerade/NAT is on (true) or off (false) for the zone.
	Masquerade *bool `yaml:"masquerade,omitempty"`

	// Permanent queries the on-disk permanent config instead of runtime.
	Permanent bool `yaml:"permanent,omitempty"`
	// Present defaults to true. Set false to assert a binding is ABSENT
	// (ignored for masquerade, which uses its own boolean).
	Present *bool `yaml:"present,omitempty"`
}

func checkFirewall(c *task.Check) Result {
	var args firewallArgs
	if err := c.DecodeArgs(&args); err != nil {
		return Result{Error: err.Error()}
	}

	set := 0
	for _, on := range []bool{args.Service != "", args.Port != "", args.RichRule != "", args.Masquerade != nil} {
		if on {
			set++
		}
	}
	if set == 0 {
		return Result{Error: "check 'firewall' requires one of: service, port, rich_rule, masquerade"}
	}
	if set > 1 {
		return Result{Error: "check 'firewall' takes exactly one of: service, port, rich_rule, masquerade (split into separate checks)"}
	}

	// Build the firewall-cmd invocation common prefix.
	base := []string{}
	if args.Zone != "" {
		base = append(base, "--zone="+args.Zone)
	}
	if args.Permanent {
		base = append(base, "--permanent")
	}

	var query []string
	var want bool      // expected presence
	var desc string    // human label for the binding
	switch {
	case args.Service != "":
		query = append(base, "--query-service="+args.Service)
		desc = "service " + args.Service
		want = presentDefaultTrue(args.Present)
	case args.Port != "":
		query = append(base, "--query-port="+args.Port)
		desc = "port " + args.Port
		want = presentDefaultTrue(args.Present)
	case args.RichRule != "":
		query = append(base, "--query-rich-rule="+args.RichRule)
		desc = "rich rule"
		want = presentDefaultTrue(args.Present)
	default: // masquerade
		query = append(base, "--query-masquerade")
		desc = "masquerade"
		want = *args.Masquerade
	}

	out, err := runCmd("firewall-cmd", query...)
	code := exitCode(err)
	switch code {
	case 0:
		// present / yes
		if !want {
			return Result{Passed: false, Detail: fmt.Sprintf("%s is present in %s, want absent", desc, zoneLabel(args))}
		}
		return Result{Passed: true}
	case 1:
		// absent / no — a clean negative answer, not an error.
		if want {
			return Result{Passed: false, Detail: fmt.Sprintf("%s is not present in %s", desc, zoneLabel(args))}
		}
		return Result{Passed: true}
	default:
		return Result{Error: fmt.Sprintf("firewall-cmd %s: exit %d (%s) — is firewalld installed/running?",
			strings.Join(query, " "), code, strings.TrimSpace(out))}
	}
}

func presentDefaultTrue(p *bool) bool {
	if p == nil {
		return true
	}
	return *p
}

func zoneLabel(args firewallArgs) string {
	scope := "runtime"
	if args.Permanent {
		scope = "permanent"
	}
	if args.Zone == "" {
		return "the default zone (" + scope + ")"
	}
	return fmt.Sprintf("zone %s (%s)", args.Zone, scope)
}
