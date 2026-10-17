# Node admission during maintenance

[ADR-1528](../adr/1528-node-admission-generation-fencing.md) validates a node's
current lifecycle and the generation captured by placement inside the instance
insert or migration ownership transaction. Active and recovering nodes admit
work. Draining, force-draining, unavailable, maintenance and retired nodes refuse
new residency. A cached active node cannot admit new work after a drain completes,
or reuse an old selection after a later cycle returns the node to active.

The capacity-policy lock precedes node/resource reservation locks. Disabled
capacity protection uses shared policy locks; enabled protection uses exclusive
locks, matching existing capacity enforcement. The node share lock holds lifecycle
and generation stable through commit. Existing operation/qualification authority
lock order remains in force before resource admission.

The database independently guards resident inserts, node changes and transitions
from nonresident to resident states. All resident states count, including warm,
frozen, snapshotting and migrating. Existing residents on the same node may finish
their current transitions and cleanup. Nonresident inventory remains writable.
Direct/legacy store calls without a captured placement perform a fresh lifecycle
check; upgraded scheduler placements also require the original selected generation.

## Upgrade

1. Pause maintenance submissions and stop all old schedd admission, job, migration
   and recovery dispatchers. Do not mix generation-aware placements with old
   dispatchers that cannot carry the selected generation across a node cycle.
   Let active handoffs settle or use the existing lease/commit recovery owner
   before taking their hosts out of service.
2. Apply prerequisites through ADR-1527, then
   `20261017140000002_node_admission_fencing.sql`. Existing inventory is retained;
   this migration creates no historical admission or evacuation proof.
3. Inspect resident rows on closed nodes. These may be historical admissions or
   work already resident when a node failed or began draining. Reconcile through
   the existing workload/recovery owners before removing a host from service.
4. Install and resume upgraded dispatchers together. Confirm a refused placement
   selects current database inventory or returns a bounded capacity result.

```sql
SELECT n.id, n.name, n.lifecycle, n.lifecycle_generation, i.state, count(*)
FROM compute_nodes n JOIN instances i ON i.node_id=n.id
WHERE n.lifecycle NOT IN ('active','recovering')
  AND i.state IN ('waking','cold_booting','running','draining',
                  'snapshotting','migrating','warm','frozen')
GROUP BY n.id, n.name, n.lifecycle, n.lifecycle_generation, i.state
ORDER BY n.name, i.state;
```

## Refusal and retry

Ordinary wakes, primes and warm-pool fills retry a definite stale-placement refusal
up to three times. Retries bypass the node cache, keep the wake identity and release
any restore-pressure reservation from the failed attempt. No VM dispatch follows
a refused insert. An insert transport error or uncertain commit response does not
authorize another insert; existing recovery handles those outcomes.

Job dispatch releases its preclaimed lease and ledger slot before retrying. The
failed transaction leaves its task queued and creates no instance. Qualification
flows preserve their reviewed private-node constraints and return a stale refusal
to their owning controller rather than escaping those placement restrictions.

Migration reads the destination before pausing the source and validates that
captured generation again at ownership commit. A destination change during adoption
uses the existing durable commit resolver: a proven abort cleans up the adopted VM,
releases its ledger reservation and cancels the source pause. Uncertain ownership
retains evidence and capacity until existing recovery resolves it. This does not
introduce a durable destination reservation before the VM RPC or change the native
migration protocol; physical evacuation still requires native fleet acceptance.

Use [generation-fenced completion](node-lifecycle-completion.md) to inspect drain
completion receipts. Do not reset lifecycle generations or bypass admission guards
to force progress. A downgrade requires stopping upgraded writers before removing
the admission guard, and ADR-1527 still preserves advanced generations/receipts.

## Verification

Memory/PostgreSQL tests pass for all resident states, closed lifecycles, obsolete
generations, same-state cycles, legacy/raw SQL, job claims and migration ownership.
Both admission/drain race orderings, enabled/disabled capacity policy locks,
rollback, suppressed statements, deferred commit failure and migration rollback
pass. The full scheduler suite passes, including fresh reselection, bounded retry,
uncertain-insert refusal, restore pressure and job lease/ledger cleanup.

Targeted memory-store and admission/completion/recovery scheduler race checks,
event/statetest race regressions, scoped production/test lint, SQLC diff and
restored-schema parity pass. Schedd links successfully. No deployment has been
performed. This environment has no KVM device; native migration and evacuation
qualification remains a separate fleet acceptance gate.
