# ADR-1644: fund app compute before execution

- **Status:** local implementation candidate; native lifecycle acceptance pending.
- **Date:** 2026-10-17
- **Extends:** [ADR-566](566-financial-visibility-and-scoped-budgets.md).
- **Decision:** support strict app-scoped gross compute `suspend_workloads`,
  zero drain, and no alert thresholds. Every matching policy must cover committed
  plus outstanding and candidate funding. Other strict policies remain drafts.

`apid` owns policy intent. `vmmd` obtains monetary authority before ordinary app
VM allocation, including cold/restore, paused warm capacity, workers, command
tasks and qualification cold boots. `schedd` remains the instance/dispatch owner.
Gateways check durable holds for warm traffic; command claims retain queued work.

Physical attempt IDs, fences and acquire request identities derive from the
persisted wake ID or command lease token, account, app and instance. The grant
pins the physical owner incarnation. A replay cannot mint fresh funding after
cleanup or a node restart. Unfenced legacy requests fail before allocation.
Intent, reservations, renewal and settlement share the account
lock. Books survive policy revisions, disable/re-enable, deletion and replacement.
Negative corrections do not create execution authority. Limits cannot be lowered
below funding already issued. Denial latches a revision-specific hold. Manual
holds cross months; `next_period` holds expire at the UTC reset.

Activation requires a quiet app, no unconfirmed leases, retained evidence from
the later of app creation or month start, complete closed sampling minutes,
known prices for positive evidence, and current native owners on all active
nodes. Positive historical compute seeds conservative commitments. Register
unbudgeted native boots too, so in-flight producers cannot race activation.
Initialize a new month's book only after prior physical leases settle and
accounting catches up. Resource or policy changes cannot discard app identity.
An existing policy can be edited before that book initializes; releasing its
hold preserves old funding and cannot bypass the next acquire's readiness checks.

Reserve two minutes of startup/execution plus thirty seconds of teardown at
the standard paid gross rate, before allowances. Free plans reserve at that
upper rate too, covering plan upgrades. Include main/sidecar RAM and VM overhead.
Ceiling each funded UTC minute and retain one millicent per minute for independent
gross allocation rounding. Commitments may exceed invoice compute; expose both
facts separately. Pin each lease's rate; a higher retained rate cannot consume a
lower-funded window. Separate monitored policies provide spending alerts.

`vmmd` renews every twenty seconds. Database time chooses windows; conservative
monotonic deadlines start from the local request start, accounting for latency.
Failed/denied renewal keeps the last deadline. Expiry cancels in-flight creation
and starts local retirement without the scheduler, meter or database. Warm/frozen
instances retain the same authority. Timeouts, missing entries and superseded
incarnations never prove retirement. Native confirmation precedes settlement
and capacity reuse. Restart recovery retries uncertain shutdown/settlement.
Cleanup of an attempt that never acquired funding first confirms native resource
absence, then records a durable cancellation under the destruction join. Delayed
acquire requests for that attempt remain fenced after policy and owner changes.
Denied grants also retain a zero-funded receipt, allowing cleanup without a VM
journal while preserving a fresh native absence check.

Funding ends before the UTC month boundary with thirty seconds reserved for
teardown. Execution can pause around reset until prior attempts settle and the
new month's accounting and funding are ready.

## Funded billing boundary

This extends the ordinary RAM-times-residency rule for strict apps: the meter
intersects residency with funded windows and confirmed settlement. Stale scheduler
rows cannot bill past funding. Unfunded strict-month VMs create no compute
charges. The retained book waives synthetic capacity-floor charges for that UTC
month, including replay after policy deletion. Actual funded residency remains
billable. Strict budgets take precedence over app/deployment floors.

Physical retirement or scheduler convergence can fail after expiry. Gregale
absorbs unfunded compute tail; the customer ceiling does not bound host resource
cost. A vmmd crash may leave a process until native recovery retires its exact
generation. Funding remains held and billing stays bounded. Native acceptance
must test this. Subscription, storage, egress, taxes, independent job resources
and builds are outside this app compute contract.

Dedicated qualification restore, qualification job boot and data-repair rehearsals
lack funding and fail closed for strict apps. Repair admission checks the original
app's budget and rehearsal containment before claiming a sandbox; held previews
cannot reserve repair capacity. Routed admission compares the node's live incarnation
with its financial authority. Roll out matching owners before activation; older
schedulers/meters must not handle strict policies.

## Validation and release

Memory/Postgres tests cover concurrent admission, ownership, replay, mutation,
settlement and UTC holds. Native-owner unit tests cover pre-allocation refusal,
local expiry and failed kill/settlement. Billing tests cover funding, unknown
VMs and price changes. API/CLI/SDK/console expose committed/reserved/remaining.

Run [native acceptance](../runbooks/financial-strict-compute-acceptance.md), the
full VM suite and leakcheck on dedicated native x86_64 Linux KVM. Compilation,
skipped metal tests or cloud checks cannot promote the strict capability.

- **Rejected:** reclaim expired funds without retirement; infer strict caps
  from delayed totals; bill stale rows beyond funding; use shared net allowance
  for app caps; silently skip unsupported VM constructors.
- **Rollback:** disable new strict policies first. Keep financial enforcement
  and recovery until every strict attempt settles; preserve financial history.
