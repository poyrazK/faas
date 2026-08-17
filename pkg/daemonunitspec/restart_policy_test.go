package daemonunitspec

import "testing"

// Restart-policy invariants (issue #593). These are asserted against
// the Registry rather than the rendered .service files so a NEW daemon
// added to the platform cannot silently ship without a restart policy
// — the generated trees are downstream of these specs.

// tlsEdge is the one daemon deliberately left on Restart=on-failure:
// a bad ACME/cert configuration fails deterministically at boot, and
// `always` would loop against Let's Encrypt until the account is
// rate-limited. Kept as a named exception so the exemption is a
// decision in the test, not an omission in a spec file.
const tlsEdge = "gatewayd-public"

func TestRestartPolicy_StateOwningDaemonsRestartAlways(t *testing.T) {
	for _, entry := range Registry {
		t.Run(entry.Name, func(t *testing.T) {
			u := entry.Unit()
			want := "always"
			if entry.Name == tlsEdge {
				want = "on-failure"
			}
			if u.Restart != want {
				t.Errorf("Restart = %q, want %q", u.Restart, want)
			}
		})
	}
}

// A clean SIGTERM-driven shutdown must not trigger a restart. This is
// what makes Restart=always safe: the drain paths return nil (exit 0)
// after a successful drain, and systemd has to respect the stop it
// just requested.
func TestRestartPolicy_CleanExitDoesNotRestart(t *testing.T) {
	for _, entry := range Registry {
		t.Run(entry.Name, func(t *testing.T) {
			if got := entry.Unit().RestartPreventExitStatus; got != "0 SIGTERM" {
				t.Errorf("RestartPreventExitStatus = %q, want %q", got, "0 SIGTERM")
			}
		})
	}
}

// Every unit bounds its restart loop, so a cascading dependency
// failure cannot become a permanent 2s-interval thundering herd.
func TestRestartPolicy_StartLimitBoundsTheLoop(t *testing.T) {
	for _, entry := range Registry {
		t.Run(entry.Name, func(t *testing.T) {
			u := entry.Unit()
			if u.StartLimitIntervalSec != "300s" {
				t.Errorf("StartLimitIntervalSec = %q, want %q", u.StartLimitIntervalSec, "300s")
			}
			wantBurst := "5"
			if entry.Name == tlsEdge {
				// Tighter on the edge: a bad cert config should
				// stop trying sooner, since it will never succeed.
				wantBurst = "3"
			}
			if u.StartLimitBurst != wantBurst {
				t.Errorf("StartLimitBurst = %q, want %q", u.StartLimitBurst, wantBurst)
			}
		})
	}
}

// TimeoutStopSec must be set explicitly and must exceed the in-daemon
// drain budget (pkg/gateway/drain.DrainGrace = 25s). Unset would
// inherit DefaultTimeoutStopSec (90s on most distros), silently
// contradicting every "30s stop budget" comment in the tree.
func TestRestartPolicy_TimeoutStopSecMatchesDrainBudget(t *testing.T) {
	for _, entry := range Registry {
		t.Run(entry.Name, func(t *testing.T) {
			if got := entry.Unit().TimeoutStopSec; got != "30s" {
				t.Errorf("TimeoutStopSec = %q, want %q", got, "30s")
			}
		})
	}
}
