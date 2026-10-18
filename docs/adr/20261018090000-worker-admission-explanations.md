# ADR: Worker admission explanations

Date: 2026-10-18
Status: Accepted
Related: [Worker recovery diagnostics](20261017223000-worker-recovery-diagnostics.md)

## Context

Worker recovery reports `waiting_capacity` for several different decisions:
account worker limits, concurrency ceilings, exhausted node resources, and an
app-wide retry cooldown. Counts alone cannot explain which action could help.
Recomputing placement from apid would observe different reservations and duplicate
admission policy. A retry event must not make an earlier refusal appear current.

## Decision

Attach bounded structured evidence at the actual scheduler admission decision.
Preserve existing RFC problem codes through `errors.As`, capacity results and all
admission guards. Evidence is diagnostic only; it never authorizes an admission,
changes retry timing or bypasses a lease. Keep it internal to scheduler calls;
no daemon protocol change is required.

Persist optional `blockers` on each environment's existing recovery observation,
under its claim, revision and completion fences. Each blocker has a stable code
and its own observation time. Numeric evidence has explicit resource units and
optional requested, used and limit values. Include usage and limits only when the
actual decision observed them: account replica clipping, app/environment
concurrency, and spend caps. A concurrent durable account refusal can omit these
numbers rather than inventing them from a later read. Stopping workers still
count against account capacity until teardown commits.

Placement records the first failing guard for each rejected candidate and
deduplicates by resource. A mixed fleet may therefore report several constraints;
available RAM on one host cannot be combined with CPU on another. Report requested
RAM, vCPU or CPU quota, including startup boost where that guard applies. Omit
host identities, fleet sizes and other tenants' usage. Distinguish a fleet with no
admitting nodes from resource exhaustion. Durable node RAM, placement generation,
listener ports, unavailable deployments and generic refusals have explicit codes.

A cooldown retains the last actual refusals for that environment with their
original timestamps and adds `retry_cooldown` with the persisted app-wide deadline.
It does not recheck physical capacity. Pools not reached after an earlier error
retain prior refusal timestamps. The account check still runs before the cooldown
gate, so its current outcome replaces any historical account refusal. A failed
complete planning/fleet read retains the previous observation. A scoped pass
replaces only its environment. A desired revision change invalidates old
evidence. Clear blockers when the target's slots
are satisfied, when demand becomes zero or when queue retirement holds the pool.
A fresh admission attempt replaces older refusals with its own outcome.

Expose the optional fields through the existing operational summary and typed
SDKs. Environment selection applies to blockers as well as counts; retry state
remains app-wide. Mark each blocker stale after the existing fifteen-minute
freshness budget, independently of the pool timestamp. Missing blockers do not
establish admission success. `gregale inspect` shows reason, numbers, units, time,
staleness and suggested action. Watch comparison ignores observation clock churn
and ordering but retains changes in reasons, values, deadlines and staleness.

Bound the persisted list to `api.WorkerAdmissionBlockersMax` (16), with unique
closed codes. No new labels or customer identifiers enter aggregate metrics.

## Compatibility and verification

The optional JSON fields reuse `worker_recovery.pools`; no new migration is
needed. The two prerequisite recovery migrations still precede schedd/apid.
Older binaries ignore the fields and may drop them on completion. Readers then
report missing explanations rather than inferring a reason from counts. Leave
the additive recovery schema in place through rollback.

Extend the existing memory/Postgres restart qualification to assert persisted RAM
refusal, original observation time during cooldown, exact retry deadline and
clearing on recovery. Exercise mixed-resource placement, non-admitting nodes,
startup CPU boost, concurrency, scoped account limits and draining account
charges. Shared store contracts cover nested copy isolation and invalid evidence.
HTTP and collector checks cover selection and read-only behavior. CLI watch and
typed SDK checks cover old responses and optional new fields. These portable
diagnostic checks do not replace the native recovery qualification.
