# ADR-447: Opt-in route-group budget consolidation

- Status: Accepted
- Date: 2026-10-02
- Related: ADR-446 (captured group plans), ADR-438 (transactional apply)

## Context

Individual family proposals can consume app rule quotas and the bounded
32-change planning allowance even when many operations need identical budgets.
Synthesizing a common prefix can reduce maintenance, but affects uncaptured and
future paths as well. Identical throttle settings cannot be merged safely:
rule identity determines bucket sharing and therefore admission behavior.

## Decision

Add an explicit `consolidate_budgets` option to group plan/apply requests and
version 3 artifacts, exposed as `gregale routes plan --consolidate-budgets`.
Reject the option for concrete version 1 requirements. Include it in plan and
idempotency fingerprints and forward it from the reviewed artifact on apply.
Omitting it retains existing planning semantics and artifact fingerprints.

Before individual allocation, consider each declared budget group and method in
normalized order. Only use its literal declared prefix with a trailing gateway
star, the platform host and one explicit method. Require at least two captured
budget violations whose independently generated allowlisted actions are
identical after combining all applicable ceilings and existing baselines. Skip
consolidation when an ordinary family selector already covers the entire batch.

Use existing priority/quota/action validation. An existing rule receives an
action-only update only when its selector already equals the group selector;
otherwise create a rule earlier than every candidate's selected budget. Never
delete rules or change existing selectors. Prove every intersecting captured
operation's selection and public request context before and after. Only the
candidate violations may change; satisfied, unassigned and unknown operations
must retain their selected budget. Public exceptions retain their policy.
Recheck the complete inventory and every overlapping requirement after each
accepted change. Unsafe or incompatible batches fall back to family proposals.

Use the existing shared node, byte, finding and change bounds, including failed
consolidation attempts. Exhaustion discards the entire batch. Export the declared
budget group, affected operations, scope beyond capture, and before/after app
rule counts plus the plan quota (including disabled rules). No original actions,
private headers or public rationale are added to exported evidence.

Apply recomputes synthesis inside the existing locked capture/policy transaction
and verifies committed configuration before saving its receipt. No new database
tables, migrations, gateway behavior or application execution are introduced.

## Consequences

Large compatible groups can fit into one budget proposal without increasing
planning limits or account quotas. The explicit option authorizes prefix scope
for uncaptured and future paths; a capture cannot establish their complete
runtime behavior. Existing shadowed rules remain counted and need separate
customer review to remove. This is conservative deterministic synthesis, not a
globally minimal policy optimizer. Throttles remain individual proposals.

## Validation

Planner tests cover large inventories at quota, exception preservation, tighter
overlaps, incompatible actions, priorities, conditional selection, unknown
families, unchanged throttle buckets, stable fingerprints and bounded work.
CLI/API tests cover explicit opt-in, export and apply forwarding, quota output,
validation and stale option/capture review. Memory and PostgreSQL transactions
verify committed coverage, rollback and idempotent replay.
