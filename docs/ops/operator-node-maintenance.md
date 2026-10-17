# Recovering operator node maintenance

Drain, force-drain, activation and retirement use
[ADR-1526](../adr/1526-atomic-operator-node-maintenance.md). Schedd commits the
lifecycle transition, immutable execution receipt, terminal intent result and
audit handoff in one transaction. A failure rolls everything back. A lost commit
reply is safe to retry with the same intent and claim: the stored result is
returned before inspecting current node state. Obsolete claims cannot act.

`succeeded` means the lifecycle request committed. A drain still requires the
existing recovery controller to migrate/recreate workloads and reach maintenance.
Completion now uses [generation-fenced cycle receipts](node-lifecycle-completion.md)
and an atomic remaining-work check, independently of the request execution receipt.
Use `gregalectl compute-nodes drain-status --node <name>` to inspect evacuation,
and the existing operator-intent status URL to inspect request execution. Failed
preconditions are terminal results; fixing the node requires a new deliberate
action. Retired nodes cannot be activated or force-drained. Retirement protects
default-local and checks live instances again during execution.

## Upgrade

1. Pause node maintenance submissions and stop every old schedd dispatcher.
   Old lifecycle writers cannot safely overlap the new protocol.
2. Apply `20261017130000001_operator_node_execution.sql` and install the upgraded
   schedulers. The migration quarantines pending and running legacy node intents
   as `outcome_unknown`; pending can include a reclaimed, already-applied action.
   Existing terminal history stays unchanged.
3. Inspect quarantined intents against node lifecycle, drain timestamps and
   recovery/migration evidence. These rows are historical ambiguous requests;
   they are never automatically reclaimed or converted into proven receipts.
   Reconcile the node before submitting a new deliberate action.
4. Resume upgraded dispatchers and submissions. Confirm new node outcomes have
   matching receipts and audit handoffs. The existing audit-outbox publisher
   delivers committed outcomes even after scheduler replacement.

Do not reset completed intents to pending or edit their receipts. Audit-outbox
pruning does not remove the execution receipt or authorize another transition.
Downgrade refuses while receipts or unresolved node intents exist; preserve the
evidence rather than deleting it to bypass that guard.

This change does not expand node-action HTTP submission idempotency. A second
request remains a new intent; the durable execution boundary belongs to each
accepted intent. VM execution remains with vmmd and the recovery controller.

## Verification

Portable memory/PostgreSQL recovery, concurrency, retirement and audit-retention
tests pass. PostgreSQL fault injection covers failed/suppressed node, receipt,
audit and intent writes, deferred commit failure, both reclaim orderings, and
capacity lock ordering with protection enabled and disabled. Scheduler restart,
lost-response and existing operator-recovery regressions, memory race detection,
scoped production/test lint, SQLC and restored-schema parity pass. No deployment
has been performed.
Existing native migration and evacuation qualification remains a separate fleet
acceptance gate; these transaction tests do not establish VM migration behavior.
