# Preview consumer delivery rate changes

Use `POST /v1/apps/{slug}/event-subscriptions/{subscriptionID}/delivery-control/rate-limit/preview`
to review proposed token-bucket settings before applying a guarded command.
This is a read-only operation requiring the existing read scope and MFA policy.
It does not change configuration, allocate command receipts, claim work, consume
tokens or reserve delivery capacity. No new migration is needed beyond the
[subscription token-bucket migration](event-subscription-rate-limits.md).

```json
{"enabled": true, "interval_millis": 100, "burst": 20}
```

All three fields are explicit. Interval accepts 10..3,600,000 whole milliseconds,
and burst accepts 1..100. The proposed bucket is projected with the same rules as
the real setter: enabling from disabled starts full, while enabled-to-enabled
changes preserve available whole and fractional tokens, capped at the new burst.
Disabling leaves the separate legacy per-second cap in force.

The response includes current and projected capacity, `expected_revision`, pause
state, legacy per-second rate, circuit state and every currently saturated
consumer/app/account live-delivery capacity scope. `projected.revision` remains
the reviewed baseline because nothing has been applied. Use `expected_revision`
with the original proposal and a newly persisted command UUID in
`SetEventSubscriptionRateLimit`. The guard protects the bucket revision only;
backlog, pause, circuit, legacy cap, capacity and token balance can change between
preview and apply. A preview is never command authorization or a reservation.

Go clients expose `PreviewEventSubscriptionRateLimit` with
`EventSubscriptionRatePreviewRequest`. Its Enabled field is an explicit boolean
pointer. Generated Node and Python clients expose the same operation. The preview
request deliberately has neither a command UUID nor an expected revision.

## Evidence scope and estimates

Production reads use one Repeatable Read, read-only transaction and one database
clock observation. The in-memory store takes one consistent locked observation.
The response samples the oldest 1,000 retained captured pending application
recipients, ordered by acceptance time and receipt identity. Processing deliveries,
unattributed publications awaiting recipient capture, future arrivals and other
consumers are outside this sample. `sampled_pending` counts the sampled rows;
`complete` means usable timestamps and no further captured pending rows in that
snapshot. It does not mean delivery is guaranteed.

`truncated` means more pending rows exist. Counts and risk identities then cover
only the sample; never interpret the modeled span as an estimate for the entire
backlog. At most 100 risk identities are returned; `risks_truncated` independently
reports omitted risk details. `deadline_risk_count` still counts all risks found
within the pending sample, including `already_expired_count`.

`rate_only_drain_millis` models the time to the last hypothetical admission of the
unexpired sample. It uses proposed token capacity, current legacy window credit,
and current next-attempt times. Each unexpired sampled recipient is assumed to
consume one admission, even if its modeled slot misses its deadline. Already
expired recipients are listed separately and consume no modeled rate capacity.
Future retries, arrivals, execution time and failure handling are not predicted.

The model uses oldest-first order for reproducibility, **not as a scheduling or
ordering guarantee**. Actual scheduling can reorder work; retries can require more
admissions; expired rows can be skipped. Consequently this estimate can be either
above or below actual drain time. It is neither an upper bound, a lower bound,
a consumer-completion ETA nor a promise of deadline safety.

`constraints` identifies current subscription pause, non-closed circuit state,
legacy pacing, saturated live slots, active leases, scheduled retry times,
truncation and unusable clocks/duration. The rate-only model includes scheduled
times and legacy pacing but deliberately does not simulate pause, circuit,
live-capacity or lease delays. Those can delay actual admission without a finite
bound. Ordering policies, backfill job controls, scheduler fairness and transport
or execution delays can add further waits; the listed constraints are not an
exhaustive readiness proof. Empty constraints and zero modeled span do not certify
that every recipient is ready.

If an enabled bucket's refill anchor, legacy window or sampled acceptance time is
in the future, or sampled timestamps are unusable, the preview becomes incomplete
and omits the duration. Unknown clocks must not be interpreted as zero delay.
Duration overflow also omits the estimate. Fresh previews can change immediately
without any configuration mutation.

## Deadline risks

New responses include `deadline_coverage: sampled_effective_pending_deadlines`,
an explicit `deadline_recipients` count and, when nonzero, `earliest_deadline_at`.
These describe all sampled effective deadlines, including expired rows, independently
of modeled admission risk. Older responses omit this optional metadata; absence
is unknown rather than zero. Pending-sample truncation still limits coverage.

Each risk item contains event source/ID, acceptance time, effective delivery
deadline, reason and an optional modeled admission time. Event payloads are never
returned. Deadlines respect captured delivery-age policy and replay age overrides;
recipients without an effective deadline do not acquire an artificial deadline.

- `already_expired`: the effective deadline is at or before the observation time.
- `modeled_deadline_risk`: the modeled admission slot is at or after the deadline.

Modeled risk is advisory, not proof that a particular recipient must expire. A
recipient with no modeled risk may still expire because of omitted waits, new
work, retries or different scheduling. Review actual pause/circuit/capacity state
and refresh the preview before applying consequential changes. No preview or
configuration change extends delivery deadlines, retention or deduplication.

The Go SDK can monitor these previews continuously through opt-in
[rate-risk watch conditions](event-delivery-rate-risk-alerts.md), with sustained
warning/recovery confirmation and the existing durable alert outbox.

For complete pending counts and approaching-deadline monitoring above this
preview's sample bound, use the [full pending summary](event-subscription-pending-summary.md).
It does not expand or replace the sampled rate-only model.

To identify individual pending deliveries by effective deadline, use the
[deadline triage listing](event-subscription-deadline-triage.md). It supports
absolute bounds and live pagination independently of the preview sample cap.
