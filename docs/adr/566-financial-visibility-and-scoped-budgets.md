# ADR-566: Financial visibility and scoped spending controls

- **Status:** implementation in progress; customer promotion requires the acceptance gates below.
- **Date:** 2026-10-02
- **Numbering:** renumbered from ADR-530 after the S3 release occupied that number on `main`. Existing financial migration IDs, SQL and original ADR comments are preserved.
- **Decision:** retain attributable usage and price history, expose read-only cost and forecast APIs, and implement customer-selected budget responses through the existing component owners. Reserve charges before execution for meters advertised as strict.
- **Why:** account overage admission alone cannot explain historical application charges or stop already-running workloads. A notification threshold, a delayed stop, and a strict monetary ceiling have different guarantees.

## Preview release scope

The first release includes retained compute/interface-egress evidence, cost and
forecast reads, and disabled budget drafts with previews and revision history.
Activation is rejected with `financial_budget_activation_unavailable`, and
previews always report `enforcement_ready: false`. Runtime enforcement, durable
notifications, holds, strict reservations, and lifecycle changes described below
remain future work. See [release scope](../financial-controls-implementation.md).

## Accounting contract

The financial ledger retains source identity, usage interval, account, application/job, project/environment, deployment, quantity/unit, price version, and source coverage. Usage and price snapshots survive workload deletion and minute-usage retention. Evidence is ingested atomically with its authoritative source or with a durable replay cursor. Replays cannot add a second charge. Closed-period corrections are append-only adjustments with original-event lineage.

Money uses checked integer arithmetic or exact rational intermediate arithmetic. Quantities are priced with their recorded contract, never today's rates. An account allowance is applied once per meter/period and allocated with a documented deterministic method. Account-wide fixed charges, credits, taxes, and unallocated costs remain explicit; their sum plus allocated costs must reconcile. Request-share route costs stay analytical estimates, separate from financial evidence. Unsupported or missing source coverage must not appear as zero cost.

During migration the ledger runs beside existing provider delivery. A coverage start marks the first authoritative retained evidence; historical quantities lacking historical prices are not silently backfilled as exact charges. Switching provider delivery to ledger results requires exact dual-run reconciliation, including plan changes, credits, refunds, and period boundaries. Budget periods follow Gregale's UTC usage allowance periods; provider invoice periods are separately reported.

## Allowance allocation

The ledger's allowance allocation retains the largest recorded account grant
within a UTC meter period. A downgrade cannot revoke that grant; it is not
granted a second time when a plan or price version changes. Allowance quantities
are shared by quantity across versions using stable largest-remainder rounding,
then net and allowance amounts are allocated to workload identities. Existing
provider delivery is unchanged during dual-run reconciliation. Promotion requires
explicit evidence for mid-period upgrade, downgrade and return-to-plan behavior.

## Policy contract

Policies have authoritative account, project, environment, app, or background-workload scopes; covered meters; a currency and monetary basis; thresholds; an enforcement mode; an action; drain deadlines; and an explicit resume rule. Account subscription/tax totals and usage overage are different bases. Mutations are authorized, idempotent, audited, and revisioned. Policy previews enumerate scope members and consequences. Notification-only policies cannot override a stopping parent budget. Changing workload membership cannot reset account spending or discard old attribution.

`apid` owns customer policy intent. `meterd` owns financial evidence and evaluates monitored thresholds. Durable policy decisions and an outbox coordinate gateways and `schedd`; Postgres notifications are acceleration only. `schedd` remains the only owner of instance state and dispatch. `vmmd` remains the only privileged VM owner. Budget holds are independent of payment, security, deletion, and user holds. Raising/deleting a budget releases only its own hold.

Stopping policies block warm request admission and new dispatch, drain within a stated bound, then park apps or terminate non-checkpointable workers/jobs through existing lifecycle paths. Floors, triggers, retries, recovery, and deployment changes cannot bypass a hold. Pending work is retained unless the policy explicitly cancels/expires it; interrupted work retains at-least-once and application-side idempotency requirements. Enforcement reports requested, draining, enforced, or failed per target, rather than claiming success when a notification was sent.

## Strict limits

Atomic admission requires committed charges plus outstanding reservations plus the candidate reservation to fit every matching strict policy. Account allowances are reserved atomically too. Execution leases cover startup, running time, and bounded teardown; denied renewal causes local deadline enforcement even if the scheduler is unavailable. An expired reservation is not reusable until work is confirmed stopped and settled. Fenced renewals/settlement and stable attempt identities prevent cross-node duplication. Strict budget checks fail closed on authoritative-state failure.

Only meters with a provable enforcement path can be offered as strict. ADR-237 direct object-storage transfers retain delayed coverage and already-issued-capability caveats. No universal hard total-invoice guarantee is implied by a strict compute budget.

Strict account budgets may use net compute overage with atomic allowance
reservation. Strict project/environment/app/job budgets use gross compute
before the shared allowance. Proportional net allocation can rise when an
unrelated workload consumes the account allowance, so a scoped net threshold
is monitored rather than advertised as strict. Traffic rejection alone does
not stop resident or background compute and cannot establish a strict compute
ceiling. `stop_previews` selects persisted preview identity; environment names
and absence of protection never establish preview identity.

A broad strict account/project/environment policy must suspend every workload
in its covered scope (`suspend_workloads`). Selectively stopping previews or
background work cannot cap continuing production work in the same scope.
Strict selective stopping is therefore limited to an app/job scope whose
authoritative workload kind supports that action. Monitored broad policies
may selectively stop previews/background work while critical production
continues; their preview explicitly reports that continuing spend.

## Delivery and acceptance

The full implementation includes persisted accounting and price history; cost/period/forecast/reconciliation APIs; scoped policies and previews; CLI, SDKs, customer console and documentation; durable notifications and enforcement; strict compute reservations and local deadlines. Capability-registry promotion follows evidence rather than merged code.

Required checks cover exact allocation/reconciliation, replay and correction handling, retained deleted-workload attribution, allowance/price changes, stale/missing evidence, UTC resets, ownership and read/write scopes, cross-node concurrent reservations, warm and cold traffic, jobs/workers/workflows, drain failures, scheduler/meter/database restarts, hold precedence, and VM resource cleanup. VM lifecycle acceptance requires native x86_64 KVM and leakcheck per CLAUDE.md.

- **Rejected alternatives:** use sampled request costs for billing; reconstruct history with current prices; equate an alert with enforcement; reject only cold wakes; conflate budgets with account suspension; release reservations on timeout without teardown evidence; promise strict limits for delayed external meters.

Rollback disables new policy enforcement before removing handlers, preserves financial history and settlements, and leaves existing provider delivery intact until its independent reconciliation gate passes.
