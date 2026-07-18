// Package flowcount — production implementation of schedd's G7 flow counter
// (spec §17). It shells out to /usr/sbin/conntrack (from the conntrack-tools
// package) to read the host's connection-tracking table once per reaper tick,
// then counts TCP flows whose source or destination IP matches a per-instance
// host-side address.
//
// Reader satisfies pkg/sched.FlowCounter — schedd's Loop consults it inside
// runReaper to fill InstanceInfo.OpenConns. The G7 rule says an instance with
// at least one open flow is considered active regardless of last_request_at
// staleness (otherwise a parked WebSocket would be reaped mid-frame).
//
// Failure semantics: fail open. Any exec / parse / context error returns
// (0, err) to the reaper, which logs and falls back to the LastRequest-only
// path. The reaper unit test TestRunReaperFlowCounterErrorFailsOpen pins this
// behavior; the production reader must satisfy it without exception.
//
// Operational notes:
//   - /usr/sbin/conntrack must be present (apt install conntrack-tools).
//     Without it, the reader returns errors every tick and G7 is silently
//     inert. The ansible role install is tracked separately.
//   - Fork+exec overhead (~5–15 ms per call) is acceptable at the 10 s reaper
//     tick for ≤100 instances. The local Runner interface is the swap seam
//     if profiling later shows the cost — replace the body with a
//     libnetfilter_conntrack-backed implementation, the FlowCounter contract
//     stays unchanged.
package flowcount
