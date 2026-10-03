# ADR-450: Opt-in route safety gates for canary advancement

- Status: Accepted
- Date: 2026-10-02
- Related: ADR-449 (automatic route checks), ADR-448 (saved requirements), ADR-122 (canaries)

## Context

Route checks report violations, but a rollout worker or manual advance can
increase traffic independently of the customer's CI checks. Historical passes
must not authorize increased exposure after captured routes or policy change.

## Decision

Store an app-owned canary route gate with mode `report` or `enforce`, a revision
and update time. Absence means report mode, revision zero. Require an explicit
expected revision for mode changes. Identical mode remains a no-op after the
revision check. Enabling enforcement requires saved version 2 requirements and
current canary/captured-contract plan entitlement. Report mode can be restored
after a downgrade. GET uses app read scope; PUT uses deploy write scope; both
require MFA. The setting is separate from route intent and evaluation hashes.

Enforce inside the state-owned AdvanceCanary transaction used by manual APID
and the meterd worker. Lock the worker lease first when required, then account,
app and existing rules in reviewed-policy order, deployments/siblings, and
capture. App/deployment parent FK locks fence new rules/captures; rule/capture
locks fence updates and deletes. Read the stored check, saved intent and current
configuration under these locks. Use the existing pure configuration fingerprint
callback to avoid an evaluator/state dependency cycle. Missing callbacks cannot
pass enforcement. Memory stores perform the same decision under their mutex.

Require complete, current, satisfied evidence for the exact candidate. Missing,
pending, running, retrying, stale, unknown, violated or unavailable evidence
blocks. A missing/stale/incomplete check coalesces into a durable refresh. When
blocked, commit only that queue request, return 409 `route_gate_blocked`, and
leave the stage, traffic, sibling statuses and traffic audit untouched. Return
bounded reason codes with a next action; do not expose private route intent.
Current violated/unknown checks do not cause endless refresh requests.

Report mode returns metadata without blocking and can request fresh evidence.
Successful advances include the decision in the API response and immutable
deployment audit. Expected route-gate conflicts count as worker waits, not
transport/worker errors; each future tick rechecks through APID.

The generic traffic API already prohibits changing an active canary's weights.
Legacy rollout recovery `advance`/`promote` cannot bypass an enforced gate; it
directs the customer to canonical canary advance. Abort, stable rollback and
existing recovery eligibility remain available without adding route evidence
requirements. `deployment advance ID --expected-step N` exposes the canonical
manual endpoint; `routes gate get|set` controls the setting.

## Consequences

This protects increases in traffic for an already active canary. Initial
activation, first deployments, arbitrary deployments, environment promotion,
and runtime application authorization remain outside this gate. A candidate
may already have its initial canary traffic share. There is no time-based check
expiry; content/configuration fingerprints determine freshness. Existing apps
keep report behavior unless customers explicitly enable enforcement.

## Validation

Memory and real PostgreSQL tests cover eligibility, revision checks, tenant
isolation, downgrade/disable, complete evidence requirements, stale passes,
queued retries, absent callbacks, legacy recovery and abort. Concurrency tests
hold a gate decision while capture writes, new rule inserts and mode changes
attempt to commit; later advances observe changed inputs. API tests cover
manual/worker enforcement, MFA/scopes, response decisions and recovery. CLI and
worker tests cover canonical advance, visible report findings and waits. Check
SQL generation, OpenAPI parity, SDK builds, documentation and scoped lint.
