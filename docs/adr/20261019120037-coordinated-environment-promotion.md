# ADR-20261019120037: Coordinated HTTP, worker and migration promotion

## Status

Implemented locally; native KVM qualification remains pending. Production flags
remain off, and no approval or signing key is provisioned by this change.

## Context

Worker and migration environment promotions previously excluded one another.
An application changing its frontend, API, worker and schema needed separate
releases despite the existing transaction publishing complete release graphs.
That separation creates an avoidable interval of incompatible application code.

## Decision

Allow `--sync-config --verify --migrate --workers` to production using the
existing captured graph, single backward-compatible HTTP-owned release command,
single qualified production push consumer, destination settings and credentials.
The worker cannot own the migration command. Existing restrictions on sidecars,
work policies, additional workers and consumer shapes remain in force.

Keep the existing durable sequence:

1. Prepare dark candidates for the complete graph from tested artifacts.
2. Admit and execute the existing runtime-attested destination migration task.
   No worker startup hold or private candidate service routing is available in
   `migrating`. Failed/uncertain commands preserve the selected baseline.
3. A proved zero exit opens a fresh full preflight deadline and candidate worker
   hold. Require current startup/configuration proof, retained baseline delivery
   health and complete HTTP dependency probes before cutover.
4. Publish the graph, configuration and new-job admission in the existing single
   transaction. Previously admitted jobs retain their original version pins.
5. Verify fresh HTTP and worker delivery windows. On qualified regression,
   recover the retained worker and restore the complete baseline graph and
   configuration. The migration receipt and expanded database schema persist;
   restoration never executes migration or reverse SQL.

Reconciliation and task/runtime fences retain their existing idempotency,
deadlines, drift rejection, lease ownership and no-replay rules. An uncertain
command requires database inspection and explicit operator resume after
scheduler-confirmed teardown. Resume retains the original task outcome and
restarts complete preflight without running SQL again. The existing resolve
action continues to require a verified non-worker replacement; widening that
replacement contract is outside this initial scope. ADR-20261019120038 extends resolution
to verified worker replacements with fresh store-owned delivery evidence.

Add the signed `coordinated` rollout capability and the reviewed
`coordinated-migration-workers` gate. It requires the existing `workers` and
`migrations` scopes and boot flags, plus `health`; bound graphs also require
`service_graph`. Separate approvals of workers and migrations cannot authorize
their composition. There is no new boot flag or automatic enablement. Existing
accepted operations and recovery are unaffected by admission policy changes.

No API wire field, schema migration, scheduler ownership change or new controller
is needed. Existing policy hashes already include both immutable contracts.

## Validation and limits

Shared memory/PostgreSQL regressions use a frontend, API and worker with distinct
source/destination bindings and secrets. Worker cases run after an attested
migration and cover startup loss, delivery health, pins, restoration, drift and
durable holds. Migration cases cover failure, uncertainty, reclaims, runtime
attestation, drift and competing task admission with the worker policy present.
Recovery cases cover resume, immutable receipts, teardown and stale intent.
API/CLI checks cover both opt-ins and compatibility with older responses;
rollout checks require explicit approval of the combined mode.

Portable task completion is not evidence that SQL ran in a real guest. The
[native acceptance runbook](../ops/coordinated-promotion-native-acceptance.md)
defines the remaining execution, database and leakcheck requirements before an
operator may approve this capability.

Local verification passed: targeted memory/PostgreSQL promotion regressions,
API admission/reconciliation and CLI contracts, rollout/controller race tests,
focused production lint, generated CLI reference freshness, environment contract,
APID size budget and ADR identity checks. Native execution remains pending.
