# ADR-20261113130000 · Post-deployment monitoring evidence

- **Status:** accepted for implementation; production activation pending.
- **Date:** 2026-11-13
- **Extends:** [receiver health](20261113110000-operator-notification-receiver-health.md)
  and [serving coverage](20261113120000-operator-serving-probe-health.md).

Add `gregalectl obs verify-deployment` as a read-only, bounded observer of the
existing serving-health and notification-health operator endpoints. Require a
full requested release SHA and explicit rollout evidence boundary. Retain
existing authorization, MFA and allowlist requirements; never mint credentials,
probe customer URLs, send notifications, deploy or roll back from the verifier.

Require fresh post-boundary source observations and recording evaluations. A
passing serving result also needs a post-boundary probe run within its own
policy age. Require nonempty independent serving intent and complete receiver
intent and evidence. Missing, stale, unsupported, unconfigured or empty evidence
cannot qualify a deployment. Historical receiver success confirmations remain
valid when freshly observed; cold routes stay unknown without sending traffic.
Notification counter windows can include events before deployment and do not
attribute a fault to the requested release.

Poll until all three checks pass or the budget expires. Derive the default wait
from the largest serving maximum result age plus the existing 120-second
evaluation freshness allowance, with a one-hour cap and 20-minute fallback.
Permit an explicit bounded override. Keep confirmed faults distinct from
unknown observations, including a report flag when a fault was seen earlier
but evidence later disappears. This is a convergence check, not a sustained
availability or human-notification-delivery qualification.

Write a versioned JSON report atomically with owner-only permissions after each
local poll. Project only bounded reviewed fields; exclude credentials, raw URLs,
addresses, upstream errors and unreviewed source strings. Label the requested
SHA without claiming the monitoring endpoints establish runtime identity.

Integrate into the canonical `cd-platform` verification job after its existing
recovery step. A new monitoring failure cannot trigger that earlier rollback.
Record the evidence boundary after full-fleet release convergence. Use pinned
SSH, a dedicated environment secret supplied through stdin, the selected
release's CLI, a loopback API and active-release pointer checks before and after
collection. Fetch the local wrapper from the workflow commit so old selected
releases fail as unknown rather than missing the reporting code. Initialize an
unknown artifact before rollout gates and attempt upload on every job outcome.
Standalone unit repair workflows retain their existing gates; callers can run
the new CLI explicitly.

Exit codes are 0 for complete passing evidence, 1 for a current confirmed fault
at the deadline, 3 for incomplete/unknown/cancelled evidence, and 2 for invalid
local options or report I/O. CI fails on every nonzero outcome and uploads the
report; no automatic recovery follows. Production activation requires the new
CLI and API, converged monitoring intent/collectors/rules, and an authorized
`MONITORING_OPERATOR_TOKEN` in each target GitHub environment. See the
[operator guide](../ops/post-deployment-monitoring.md).
