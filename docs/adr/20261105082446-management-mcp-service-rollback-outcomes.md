# ADR-20261105082446: Durable service rollback acceptance recovery

## Status

Accepted — 2026-11-05. Extends ADR-20261105000513 and ADR-601.

## Context

Checked service rollback already commits durable intent, target readiness and a
stable handoff identity together. Request rollback can recover that acceptance
through an original attempt UUID, but service rollback cannot: a lost reply and
an expired bridge plan leave the agent without an exact native operation lookup.
Current traffic or a later handoff cannot prove which original request committed.

## Decision

Extend the optional `Rollback-Attempt-ID` contract to exact manual **default-scope
service** rollbacks. The attempt UUID is the existing checked operation ID.
The current/target pair and reason retain their canonical request fingerprint.
Workers, named environments, alert attribution, release acknowledgement and
database restoration remain outside this additive header contract. No new table,
endpoint, DTO or mutation tool is introduced.

Both native stores check prior attempts under the app lock before mutable target
eligibility, readiness or notification writes. A matching original request stays
accepted even after the operation fails; request/service mode changes cannot
revive the original attempt. A different
request or a nonmanual ledger entry conflicts. Fresh intents still require a
supported workload mode and default scope. Existing owner, serving CAS, release,
artifact, contract and binding guards remain authoritative. Accepting a service
operation does not finish gateway ACKs, drain the predecessor or prove health.

Before its first POST the bridge atomically persists its attempt marker, history
and minimal encrypted lookup in `service-rollback-outcome-lookups/`. Its binding
has a service-specific tag. Request rollback references retain their original
namespace and format, so N-1 readers never interpret a service lookup as a
request receipt. Older writers preserve the opaque namespace; a current bridge
bounds it by retained history and removes it on expiry or grant revocation.
Ambiguous namespace collisions and malformed snapshots fail closed. Previously
attempted legacy plans receive no replacement identities or inferred acceptance.

`gregale_get_action_outcome` reads the exact native operation and verifies its
original UUID, immutable app, default scope, service marker, exact deployment
pair, reason fingerprint, manual provenance and creation time. Verified output
contains only redacted acceptance plus separate exact `observation` (rollback
operation), `rollout_observation` (target handoff/drain) and `health_observation`
(app readiness) calls. These calls are not executed automatically. Missing or
mismatched evidence stays unresolved and never proves non-commit.

History, existing recovery prompts and resources expose the same lookup after
restart and plan expiry, including with writes disabled. Final guards recheck
current read consent, immutable target identity, storage, retention and the
original reference. Reading never rewrites recorded history, extends plan expiry,
reconstructs a mutation or grants retry authority. Catalog counts do not change.

## Consequences

Native acceptance and completion remain separate observations. A failed service
operation can have a verified acceptance receipt; later serving traffic cannot
replace its original evidence. Rolling APIs that ignore the additive header can
return a different operation UUID: recovery reports uncertainty rather than
searching for or replaying a similar request. Named-scope and environment-release
rollback recovery remain separate work.

## Validation

Required checks cover concurrent Mem/Pg attempts, failed insertion atomicity,
stale serving CAS, terminal replay, foreign ownership and unsupported workers;
HTTP lost replies and read-only operation lookup; both MCP protocols, encrypted
restart, expiry, consent/identity races, independent observations, namespace
compatibility, legacy non-backfill, pruning and revocation. Typed client
transports and pinned real Codex profiles exercise the same recovery without
model requests or native writes in the read-only fixtures. Native/provider
execution qualification remains separate from portable fixtures.
