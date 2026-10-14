# ADR-1242: Observe MCP account policy convergence after release

Status: Accepted

## Context

Promotion admits one gateway-policy snapshot and retains the evidence. Subsequent
restarts, membership changes and stalled replay need an operational signal without
requiring another deployment or making the alert evaluator a traffic writer.

## Decision

Add opt-in, app-anchored metrics backed by one PostgreSQL statement snapshot:
`mcp_policy_unconverged_gateways` counts distinct serving gateways with missing,
stale, future-dated or behind observations; `mcp_policy_unverified` is one when an
active HTTP deployment has no serving fleet or no established account revision.
The account revision includes sibling-app rules and the unknown-owner legacy
floor. Other accounts' known mutations do not advance it. A target with no active
HTTP deployment is paused; absent source support or failed reads are degraded.
Unverified data cannot resolve the unconverged-gateway metric as healthy.

Reuse the existing meterd evaluator, claim deduplication, cooldowns, delivery
retention, rule states, and `alert.resolved` audits. The metric is instantaneous;
`window_spec` is not a sustained-breach duration. Configured alerts run at the
existing evaluator cadence. New metrics support webhook notifications only,
enforced by API validation, database constraints and the evaluator. They never
change traffic or instance state. No rules or outbound notifications are enabled
by this implementation itself.

Capture investigation metadata from the same observed snapshot, including the
current serving deployment, account policy status path, and latest accepted MCP
admission UUID/history path when retained evidence exists. Missing/expired evidence
does not stop monitoring. Payloads contain no JWT actions or credentials.

Enrich account-scoped policy status with the ongoing snapshot and display it,
with enabled convergence rule states, in MCP status and overview. Alert-read
permissions are reported independently from observed gateway state. Protocol
qualification remains a separate result. Overview is degraded for pending replay
and unknown for unverified policy if otherwise healthy. Recovery reads active
once fresh replay is established. A legacy server can still supply coarse account
edge-rule status without admission correlation.

## Consequences

Two presets simplify explicit setup, subject to existing plan quotas and webhook
configuration. Sustained breaches may refire after cooldown, following existing
alert semantics; concurrent evaluators and ticks inside cooldown do not duplicate
a delivery. Monitoring is observation, not live OAuth enforcement or a lease on
membership. Status observations and the broader policy response may be sampled at
different instants; the MCP snapshot's captured time is authoritative for its data.
