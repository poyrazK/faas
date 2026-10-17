# Financial controls local implementation candidate

This release candidate includes the implemented financial visibility and
budget-planning surfaces from ADR-566, active monitored notifications and
compute-only `stop_previews`, strict app/job gross compute reservations, and shared
account net compute funding optionally combined with interface egress. This branch
is a local release candidate; native VM acceptance and publication are still
required. Other stopping actions and strict scopes remain deferred.

## Customer-visible scope

- Retained compute and interface-egress evidence, historical price contracts,
  shared allowance allocation, attributed costs and append-only corrections.
- Read-only costs and forecast APIs, Go client, CLI, Node/Python SDK contracts
  and console costs with explicit freshness and coverage gaps.
- Revisioned budget policies and drafts through the API, CLI and console.
- Active monitored notification policies, retained evaluation health, atomic
  threshold outbox events and fenced/retryable email delivery. The console
  creates account/app/job compute policies and combined account compute/egress
  policies; API/CLI also support monitored policies across existing scopes.
- Latched preview stop decisions, independent of payment/security/user intent;
  warm HTTP and service/TCP admission, queued invocation/command claim gates,
  scheduler drain/teardown retries and per-target acknowledgements. Preview
  command VM ownership and preparation deadlines survive scheduler restart.
- Atomic app/job gross compute and shared account compute/egress funding,
  fenced native execution leases, kernel byte quotas, local deadlines, durable
  recovery, funded billing windows and conservative balances
  in the API, CLI, SDKs and console. See [strict compute decision](adr/1644-strict-app-compute-reservations.md).
- Account ownership, usage-read/admin authorization, MFA, bounded idempotency
  keys and optimistic revisions. Recovery reads remain available during billing
  suspension without changing payment status.

## Release limits

Budget activation accepts `mode=monitored` with `action=notify`, or
`action=stop_previews` with the compute meter alone. Strict app/job `gross_usage`
compute and account `net_usage` compute (optionally `[compute, egress]`) support
`suspend_workloads`, zero drain and no alert thresholds. Initial activation
requires quiet covered workloads, complete accounting, recorded account
allowances, and capable native owners. Combined policies check the sum of both
meters against one paid limit with separate quantity allowances. Other enabled
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

The release excludes other stopping actions, strict project/environment scopes,
app/job net reservations, scoped egress-only reservations and full-invoice hard
ceilings. Byte credits include a framing margin; unused or uncertain credits
commit in full after physical retirement. Budget commitments can exceed invoice
usage, which continues to use measured bytes. A funded period pins its recorded
egress rate ceiling; incompatible pricing changes must wait for a new period. Egress funding also
pins its lowest recorded allowance; increases benefit funding in a new month.
Provider events are held if pricing, allowances or sampled quantities exceed
the funded contract.
See [shared egress funding](adr/1642-account-strict-egress.md). A monitored preview budget can be exceeded during
evidence ingestion, scheduler sweeps, the configured drain interval, and bounded
VM teardown. Broad scopes leave production spending explicitly continuing.

Stop decisions latch after fresh known costs reach the threshold, including a
labelled partial subtotal. Missing or stale evidence cannot create a stop and
never releases an existing stop. New previews are checked against the same
latched decision. Manual holds persist across UTC months. A policy mutation
releases only its own revision; re-enabling can immediately cross again against
existing spending. An in-flight teardown may finish after a policy edit.

The scheduler reports requested/draining/enforced/failed, with stale observations
projected as unavailable. Enforced requires confirmed teardown and settlement
for all ordinary and command VMs in that observed target. Legacy commands
without retained node ownership report failed rather than claiming success;
drain old command sessions before enabling this release. A preparing command
keeps its reservation until its RPC reply or persisted preparation deadline plus
clock grace, followed by confirmed teardown. Queued work remains pending unless
its existing customer-selected expiration/cancellation policy applies.

## Acceptance preparation

The local backend also includes a dedicated full-daemon financial lane and
real-meterd/provider reconciliation fixtures. They check uncertain provider
replies, failed receipt writes, cumulative-counter replay, exact invoice versus
commitment bounds, native funded retirement during database loss and owner
restart. A source-derived JSON verdict includes final leakcheck, service
restoration, host facts and artifact hashes. See the
[full-daemon lane](runbooks/financial-strict-compute-acceptance.md#full-daemon-financial-lane-and-provider-reconciliation)
for commands and evidence. A local compilation or portable pass does not close
native, physical two-host, full workload/performance or external-provider gates.

The local implementation now also includes a guarded
[physical two-host qualification](runbooks/financial-two-host-qualification.md)
runner and CI wiring. It uses a real remote native owner, per-host SQL network
cuts, retained credits, owner restart, source-derived component selection and
cleanup evidence from both hosts. The full-daemon CI job retains and rechecks
raw provider/receipt vectors. These are implementation and execution gates;
the required remote HTTP payload evidence, the full workload matrix and performance still block
promotion. No physical two-host execution has been performed locally.

ADR-1643 adds public forwarding through the native-recovery owner's funded
interface for HTTP, Upgrade, TCP and UDP, with captured-interface checks and
separate authorized private bridges. The full-daemon report now requires v1/v2
streamed payload measurements, quota exhaustion without the DB, private
isolation and owner restart, plus exact bridge binary/supporting-source hashes.
These local changes prepare native evidence; they do not supply that evidence.

The physical-pair runner now also prepares a source-bound, 14-phase gateway
transport gate. Production HTTP/H2C, Upgrade, TCP and UDP forwarding crosses
fixture-authenticated TCP to node B's production VMMD RPC server and its real
funded native Manager. Tests compare complete streamed payloads with the same
canonical interface counter, including custom main and sidecar-routing ports,
real SQL partitions, quota exhaustion, trusted private forwarding, physical
deadline retirement and owner replacement. Supporting sources, all bridge
helpers and the multi-protocol guest fixture are bound to both host records.
The targets are pre-admitted by the fixture; full daemon/API/scheduler admission,
registered-node PKI and sidecar process lifecycle remain separate gates. These
tests have not executed on dedicated KVM hosts in the local workspace.

## Rollout and rollback

1. Review and merge the platform preview PR after its migrations, accounting,
   API/CLI/SDK and repository CI checks pass.
2. Before publishing, run the native gates in
   [preview stop acceptance](runbooks/financial-preview-stops-acceptance.md) and
   [strict compute acceptance](runbooks/financial-strict-compute-acceptance.md).
   Publish the ordinary signed platform release from the reviewed main commit
   using the existing release workflow. Apply the retained-history, monitoring, stop and command-ownership migrations
   before exposing the console release.
3. Let meterd capture current price contracts and fresh usage evidence. Old
   periods without recorded prices stay incomplete. Verify account isolation,
   freshness/coverage, one retained sample replay, revision handling, notification activation and preview-stop activation, strict app/job/account funding, combined compute/egress byte exhaustion, and
   unsupported activation refusal.
   Exercise a threshold, replay, mail failure/retry and expired claim recovery;
   verify that partial/stale evidence stays visible and sampling remains healthy.
4. Release the console only after the platform APIs answer successfully.
5. Remove the console surfaces first if rollback is needed. Preserve retained
   financial tables, migrations and audit history; do not drop populated
   evidence. A platform rollback must retain the additive migration set, or
   verify compatibility of the older binary against the migrated schema.
   Retain funded billing and native recovery until every strict lease settles.
   Existing provider delivery remains in place throughout this release.

Production publication, deployment and operational smoke evidence are tracked
by the release PRs and existing release runbook. Local checks alone do not prove
a deployed release.
