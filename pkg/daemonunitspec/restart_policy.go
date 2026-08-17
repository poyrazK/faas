package daemonunitspec

import "github.com/onebox-faas/faas/pkg/daemonunit"

// Platform-wide systemd restart policy (issue #593).
//
// Every other field in a UnitXxx() literal is genuinely per-daemon —
// its user, its slice, its memory ceiling, its capability set. The
// restart policy is not: it is one platform decision that happens to
// be written into nine unit files. Keeping it here rather than
// copy-pasted into each spec means tightening it fleet-wide is a
// one-line edit, and — more to the point — a daemon added later
// cannot ship without a policy by forgetting to paste a block.
//
// The values:
//
//   - Restart=always for state-owning daemons. on-failure ignores
//     exit 0 by definition, so a daemon that exits cleanly for an
//     unintended reason (a recovered panic that returns nil, a stray
//     os.Exit(0)) stays down until someone runs `systemctl start`.
//
//   - RestartPreventExitStatus=0 SIGTERM is what makes `always` safe
//     rather than a restart loop on every deploy: the drain paths
//     return nil after a successful drain, and systemd must respect
//     the stop it just requested.
//
//   - StartLimit* bounds the loop. Without it, RestartSec=2s retries
//     forever, so a cascading failure — one Postgres blip every
//     daemon sees at once — becomes a permanent 2s-interval
//     thundering herd against the sick dependency. After the burst,
//     the unit goes `failed` and stops trying, which is the state
//     that actually pages an operator.
//
//   - TimeoutStopSec=30s must exceed the in-daemon drain budget
//     (pkg/gateway/drain.DrainGrace is 25s, sized as "30s unit budget
//     minus 5s kernel-reap headroom"). Unset would inherit
//     DefaultTimeoutStopSec (90s on most distros), silently
//     contradicting every 30s stop-budget comment in the tree.
//
// The StartLimit pair lands in [Unit], not [Service] — systemd moved
// them in v229 and ignores a [Service] placement with a warning.
func withRestartPolicy(u daemonunit.Unit) daemonunit.Unit {
	u.Restart = "always"
	u.RestartSec = "2s"
	u.RestartPreventExitStatus = "0 SIGTERM"
	u.TimeoutStopSec = "30s"
	u.StartLimitIntervalSec = "300s"
	u.StartLimitBurst = "5"
	return u
}

// withEdgeRestartPolicy is the TLS edge's deliberate divergence
// (gatewayd-public). A bad ACME/cert configuration fails
// deterministically at boot: Restart=always would loop against Let's
// Encrypt until the account is rate-limited, which turns a
// misconfiguration into an outage that outlives the fix. It keeps
// on-failure and a tighter burst — an edge that cannot start should
// stop trying sooner, because it will not succeed.
//
// Everything else (the clean-exit guard, the stop timeout, the
// interval) is the platform policy, so this composes on top rather
// than restating it.
func withEdgeRestartPolicy(u daemonunit.Unit) daemonunit.Unit {
	u = withRestartPolicy(u)
	u.Restart = "on-failure"
	u.StartLimitBurst = "3"
	return u
}
