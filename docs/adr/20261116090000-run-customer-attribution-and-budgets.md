# Run customer attribution, history and admission budgets

Status: implemented and functionally verified on the internal KVM node; preview writers remain off. Native production eligibility is a separate gate.

## Context

Named tasks and durable Agent continuations need customer attribution and
bounded compute delegation. A caller-supplied label cannot establish an access
boundary, and a budget checked outside atomic admission can oversubscribe under
concurrent requests or count a lost-acknowledgement retry twice.

## Decision

Account administrators configure customer policies and bind public API key IDs
or owned Agent endpoint IDs to a customer reference and optional agent reference.
Key bindings resolve internally to the existing stable key family, so rotation
preserves the binding without exposing that private identity. Ordinary Runs
credentials cannot create bindings or select another customer. Account-wide
callers may explicitly select an existing customer; authenticated bindings win
and conflicting attribution is refused. Attribution is control-plane metadata,
captured at admission and excluded from guest payloads.

Customer policies apply to raw source Runs and named tasks. Task policies apply
across all versions of that name. Both policies are checked under the existing
account admission lock, after durable replay and before any Run, artifact grant
or reservation commits. Nil limits add no restriction; zero refuses new work.
Policies bound concurrency, daily invocation count and daily compute MB-ms, and
have an admission kill switch. This does not cancel already admitted work.

Reserve `(memory_mb + PerVMOverheadMB) * timeout_ms` before admission. An immutable
execution-ID-keyed reservation joins the existing terminal usage ledger to
settle to `min(reserved, (memory_mb + PerVMOverheadMB) * wall_time_ms)` once.
This is an execution planning allowance, not an invoice or monetary spend cap.
Missing terminal ledger evidence retains the full reservation and active slot.
The reservation freezes the memory/overhead coefficient for later settlement.
PostgreSQL assigns the UTC day from its own clock when admission checks run; concurrent slots include older
in-flight Runs, while daily counts/compute stay with their admission day.
Changing or disabling a policy does not reset that day's usage. Rebinding never
rewrites historical attribution. Configuration uses expected revisions;
binding removal keeps a revisioned tombstone to prevent stale-write reuse.

Thresholds at 80% and 100% of configured limits create one durable alert per
account/policy/day/metric/threshold. Admission publishes metadata-only
`execution.budget.threshold` obligations through the existing signed account
webhook delivery ledger. Alerts include reservations and describe admission
pressure, not finalized monetary cost. Receiver management remains account-only.
Budget refusals return a structured `run_budget` problem extension. Managed
Agent tool refusals remain unadmitted HTTP errors, so a later retry can admit
that same call without conflicting with an earlier terminal inbox result.

History uses cursor pagination and filters by task/version, customer, agent,
status and time. It preserves existing account/key-family read boundaries and
returns metadata, receipt links and terminal usage without source, inputs,
results or logs. Existing receipt/event APIs retain their authorization and
replay contracts. Elapsed time is exposed without inventing per-Run startup
timings that are not currently stored.

## Compatibility and rollout

Add sidecar tables without changing existing execution/ledger layouts. Expand
and validate the account receiver vocabulary first; retire its older restrictive
constraint in the release contract phase before enabling threshold subscriptions. Existing
readers and terminal writers remain compatible. A database admission fence
refuses an N-1 admission writer for any account with configured controls; new
writers announce the protocol inside their admission transaction. Rolling back
must preserve a compatible admission writer or disable new admissions, never
silently bypass policies. Deploy data before compatible control-plane writers.
Task admission remains behind its existing default-off preview gates.

Accounting starts with reservations recorded by compatible admission writers.
Finish older untracked Runs before enabling the first task policy during an
upgrade. Receipt history remains readable; allowance totals cover recorded
admissions rather than reconstructing pre-upgrade usage.

## Acceptance

Exercise memory and PostgreSQL stores for concurrent admission, lost-ack replay,
raw-source bypass refusal, key rotation, endpoint/customer ownership, binding
revision/tombstone behavior, cross-version task limits, UTC-day accounting,
failed/queued-cancelled completion, missing-ledger conservatism, threshold dedupe
and rollback. API/CLI/Go/Node/Python contracts must preserve attribution,
filters, revisions and idempotency. The functional task/Agent lane must prove an
admitted Run can finish and resume its Agent after controls disable new work.
Functional internal-node evidence does not confer native production eligibility.

The [2026-11-16 verification](../ops/evidence/20261116-run-controls/README.md)
records passing state/race/API/CLI/SDK contracts, five Linux lint groups and the
exact-source real-VM journey. One Run and one durable inbox continuation survive
policy disablement, task retirement and actual process restarts; two unadmitted
budget refusals leave no terminal inbox result. Customer and task usage settle
to matching positive compute with zero active slots and reservations. The
attempt history preserves the rejected symlinked fixture placement before the
passing real-directory retry; runtime path guards remain unchanged.
