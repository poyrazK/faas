# Financial visibility preview release

This release candidate includes the implemented financial visibility and
budget-planning surfaces from ADR-566. The full scoped-enforcement implementation is preserved
on `codex/financial-controls-20261002` and is deferred for a later release.

## Customer-visible scope

- Retained compute and interface-egress evidence, historical price contracts,
  shared allowance allocation, attributed costs and append-only corrections.
- Read-only costs and forecast APIs, Go client, CLI, Node/Python SDK contracts
  and console costs with explicit freshness and coverage gaps.
- Disabled budget draft creation, editing, deletion, scope preview and retained
  revision history through the API, CLI and console.
- Account ownership, usage-read/admin authorization, MFA, bounded idempotency
  keys and optimistic revisions. Recovery reads remain available during billing
  suspension without changing payment status.

## Release limits

Budget activation returns HTTP 422 `financial_budget_activation_unavailable`.
Drafts neither stop workloads nor send spending notifications. Preview results
report `enforcement_ready: false`. Financial totals cover recorded supported
usage only; invoice reconciliation, fixed fees, credits/taxes, unsupported
meters and uncovered history remain explicit gaps. Existing provider billing
and payment, security and workload lifecycle behavior remain authoritative.

The release excludes new scheduler/gateway enforcement, monetary reservations,
financial invocation/source claims, command VM ownership and teardown, budget
notification delivery and budget runtime/action APIs. Those changes stay on the
full implementation branch.

## Rollout and rollback

1. Review and merge the platform preview PR after its migrations, accounting,
   API/CLI/SDK and repository CI checks pass.
2. Publish the ordinary signed platform release from the reviewed main commit
   using the existing release workflow. Apply the four financial migrations
   before exposing the console release.
3. Let meterd capture current price contracts and fresh usage evidence. Old
   periods without recorded prices stay incomplete. Verify account isolation,
   freshness/coverage, one retained sample replay, draft revision handling and
   the activation refusal.
4. Release the console only after the platform APIs answer successfully.
5. Remove the console surfaces first if rollback is needed. Preserve retained
   financial tables, migrations and audit history; do not drop populated
   evidence. A platform rollback must retain the additive migration set, or
   verify compatibility of the older binary against the migrated schema.
   Existing provider delivery remains in place throughout this release.

Production publication, deployment and operational smoke evidence are tracked
by the release PRs and existing release runbook. Local checks alone do not prove
a deployed release.
