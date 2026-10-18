# ADR-1175: Subscription capacity-pressure diagnostics

Status: Accepted
Date: 2026-10-13
Related: ADR-614, ADR-962, ADR-1174

## Decision

Add optional capacity_pressure to the existing subscription delivery-health read.
Existing capacity_waits provides complete effective waiting-reason counts but does
not explain which live ceiling is full or how much work occupies each scope.
Reuse trusted delivery slots and existing backlog waiting-reason rules rather than
infer current pressure from recorded deferrals or the bounded pending sample.

Provide a timestamped snapshot with owned app/subscription binding, coverage,
complete, configured consumer ceiling, current blocking_scope, and exactly three
scopes in consumer/app/account admission priority order. Each scope includes live
pending-plus-dispatching delivery usage, effective ceiling, at_limit, this
subscription's effective capacity-waiting recipient count, oldest waiting event's
acceptance timestamp and acceptance age, and informational operator advice.

Live consumer usage applies only to this subscription; app/account usage includes
siblings in those scopes. Consumer ceilings use ADR-1174's configured value clamped
to the current plan. App/account ceilings remain plan limits. Include replay
slots and exclude untagged legacy invocations as in ADR-614. blocking_scope is the
first currently full ceiling in admission order; empty means none observed. Other
full scopes remain visible. This does not guarantee every recipient would be
admitted: filters, duplicates, cancellation, ordering and other controls still
apply, and duplicate/cancellation/keep-latest exceptions remain unchanged.

Waiting counts include all retained pending application recipients accepted by
the observation time, not a sample or the health history window. Apply existing
backlog control/ordering/routing precedence before classifying capacity reasons.
Whole-event active lease handling follows the same SQL backlog rule. A recorded
capacity wait can remain after live usage drops; do not label that as a current
full ceiling. In that case advice suggests refreshing rather than raising limits.
For full scopes, advice suggests inspecting/draining the relevant backlog and
reviewing a consumer ceiling only within the plan allowance. Raising a subscription
ceiling cannot remove app/account limits. No advice executes a control or recovery.

Age is explicitly since event acceptance, not continuous capacity-wait duration.
The first instant of continuous capacity waiting is not retained. Omit oldest
accepted timestamp and use zero age when that scope has no waiting recipients.
Do not fabricate a wait start from updated routing timestamps or resettable
capacity deferral records.

## Consistency and compatibility

Read ownership, plan, configured ceiling, slot counts and effective waiting counts
inside one PostgreSQL read-only repeatable-read transaction; no row/advisory locks
or control writes. MemStore reads under its mutex using the same count and backlog
observation helpers. Set complete only after the full aggregate read succeeds;
errors fail the combined health request. Stores lacking the optional pressure
interface omit the field instead of producing fabricated zero pressure.

The overall delivery-health response still combines independent reads. Pressure's
observed_at identifies its separate snapshot; it can differ from capacity_waits or
waiting_reasons obtained earlier. Pressure is advisory, does not reserve capacity,
promise immediate admission, alter retries/retention/order/deduplication, or change
watch alert eligibility. Handler delivery remains at least once.

Go, Node and Python SDKs expose the additive optional models, and the existing CLI
subscription-delivery-health command prints scope usage, limits, waiting acceptance
age and relevant advice. Use the existing route, authorization and deadlines. No
migration, schema, quota, registry entry or new SDK route mapping is needed.
Validate builds, SDK generation/source parsing, SQLC parity and static checks;
no local tests are added or run. Database behavior remains for CI qualification.
