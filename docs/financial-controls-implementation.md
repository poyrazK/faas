# Financial visibility and monitored notifications preview

This release candidate includes the implemented financial visibility and
budget-planning surfaces from ADR-566, plus active notification-only monitoring.
Scoped workload stopping and strict reservations remain deferred.

## Customer-visible scope

- Retained compute and interface-egress evidence, historical price contracts,
  shared allowance allocation, attributed costs and append-only corrections.
- Read-only costs and forecast APIs, Go client, CLI, Node/Python SDK contracts
  and console costs with explicit freshness and coverage gaps.
- Revisioned budget policies and drafts through the API, CLI and console.
- Active monitored notification policies, retained evaluation health, atomic
  threshold outbox events and fenced/retryable email delivery. The console
  creates account/app compute policies; API/CLI support every existing scope.
- Account ownership, usage-read/admin authorization, MFA, bounded idempotency
  keys and optimistic revisions. Recovery reads remain available during billing
  suspension without changing payment status.

## Release limits

Budget activation accepts only `mode=monitored`, `action=notify`. Other enabled
responses return HTTP 422 `financial_budget_activation_unavailable`. Disabled
drafts neither stop workloads nor send spending notifications. Notification
policies report active monitoring while `enforcement_ready` remains false. Financial totals cover recorded supported
usage only; invoice reconciliation, fixed fees, credits/taxes, unsupported
meters and uncovered history remain explicit gaps. Existing provider billing
and payment, security and workload lifecycle behavior remain authoritative.

Scope previews also require retained attribution coverage. For example,
app-wide synthetic warm-capacity charges do not identify an environment.
An environment preview excludes those charges from its known subtotal and
reports partial coverage with `compute:missing_scope_attribution`; it does not
present an unallocated amount as complete zero spending. Account and app
subtotals still include the retained charges.

The release excludes new scheduler/gateway enforcement, monetary reservations,
financial invocation/source claims, command VM ownership and teardown, and
budget workload-action APIs. Those changes require their separate acceptance gates.

## Rollout and rollback

1. Review and merge the platform preview PR after its migrations, accounting,
   API/CLI/SDK and repository CI checks pass.
2. Publish the ordinary signed platform release from the reviewed main commit
   using the existing release workflow. Apply the retained-history and monitoring migrations
   before exposing the console release.
3. Let meterd capture current price contracts and fresh usage evidence. Old
   periods without recorded prices stay incomplete. Verify account isolation,
   freshness/coverage, one retained sample replay, revision handling, notification activation and stopping/strict activation refusal.
   Exercise a threshold, replay, mail failure/retry and expired claim recovery;
   verify that partial/stale evidence stays visible and sampling remains healthy.
4. Release the console only after the platform APIs answer successfully.
5. Remove the console surfaces first if rollback is needed. Preserve retained
   financial tables, migrations and audit history; do not drop populated
   evidence. A platform rollback must retain the additive migration set, or
   verify compatibility of the older binary against the migrated schema.
   Existing provider delivery remains in place throughout this release.

Production publication, deployment and operational smoke evidence are tracked
by the release PRs and existing release runbook. Local checks alone do not prove
a deployed release.
