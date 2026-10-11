# ADR 990: Fenced durable entity quota migration

Tenant admission only reserves entities as they are invoked. Dormant historical
state therefore needs reservation coverage before ingress returns on a populated
scope. Bucket listing is paginated and cannot share a transaction with entity
manifests or a separate checkpoint.

Store the migration UUID, revision, policy, cursor and phase in the existing
tenant quota CAS record. Start holds admission and waits the maximum entity lease
plus invocation budget. Each bounded page atomically commits reservations and
checkpoint advancement. Replacements compare the current UUID/revision, retain
reservations and fence stale workers. Unknown writes require checkpoint reads;
no blind retry or rollback is allowed. A separate full verification rotation must
observe usage within caps and reservation coverage before resuming admission.

Planning is read-only and page-scoped. Missing usage, legacy environment identity,
corrupt discovery, cap overages and budget shortfalls block progress. Migration
does not delete data, release capacity, increase limits or dispatch application
code. Production ingress stays off while operators upgrade and quiesce all writers.
Older binaries or quota-disabled nodes do not honor the hold; listing completeness,
clock bounds and native storage/failure acceptance remain external prerequisites.

Expose opt-in, owned-app authenticated HTTP planning/status/start/step operations.
Read scope can inspect; deploy-write scope and normal recovery eligibility can
apply. Completion reports observed coverage, not native acceptance or a fleet
acknowledgement. The readiness endpoint retains its external unverified checks.

See [workflow and acceptance handoff](../durable-entity-quota-migration.md).
