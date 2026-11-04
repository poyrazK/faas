# ADR-20261104033000: MCP release protocol gates and verification budgets

## Status

Accepted; extends the MCP hosting profile and ADR-20261104020000.

## Context

Native Task releases only qualified exact replicas when a role declared optional
authorization expectations. A modern-only endpoint could pass catalog discovery
while failing initialization, then be promoted before previous workers were
parked. Ordinary MCP releases shared one 30-second verification context across
sequential role and replica checks, rejecting healthy supported role counts.
Node observer compatibility metrics also hardcoded a three-attempt budget and
included retries that were not due yet.

## Decision

Always qualify every native candidate replica and verify public readiness for
every selected role before promotion and retirement of previous workers. Protocol
qualification is independent of optional catalog and authorization expectations.
When no role policy is supplied, use the release's primary client token for the
default role. Retain exact instance identity, stable replica-set and catalog
agreement checks. Apply the same gates after promotion and during restoration.

Schedule independent role/replica checks with four concurrent diagnostic jobs.
Give each job its own 30-second deadline after it starts. Each job owns its
protocol client and result slot; assemble receipts in stable policy/replica order.
Keep the exact-instance per-request limits. Ordinary deployment verification uses
the configured deployment wait timeout as its overall budget after candidate
builds and pinning. Native verification retains the adapter's plan deadline.
Parent cancellation cancels queued and active probes; incomplete checks fail
closed. These scheduling bounds are client diagnostic protections, not quotas.

Pass Node's configured attempt budget into observer, worker metrics and doctor
checks. Count incompatible work only when its retry is due or its running lease
has expired and its attempt remains eligible. Input/drain resumptions continue
their current attempt. Keep capacity-waiting and outstanding gauges independent
of handler compatibility, and report future retries through retry-waiting gauges.
Observers remain read-only and never decrypt Task payloads.

## Consequences

Native protocol failures cannot promote a candidate or park previous workers.
Large valid role sets no longer consume each other's probe budget, while network
concurrency and cancellation remain bounded. PostgreSQL tests cover custom retry
budgets, due times and resumptions. Local HTTP regressions cover failed and
stateful replicas, public readiness, successful multi-role deployment, stable
receipts and cancellation. Native KVM and real OAuth qualification remain
separate acceptance gates.
