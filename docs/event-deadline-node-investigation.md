# Investigate a Node deadline alert

`investigateEventDeadlineAlert(checkpoint, reference, options)` from
`@gregale/sdk-node` returns one current read-only triage page for an exact retained
alert. Configure SDK authentication normally, then load the Node checkpoint and
choose its alert by `{ incidentId, sequence }`:

```ts
import { investigateEventDeadlineAlert, validateEventDeadlineCheckpoint } from '@gregale/sdk-node/preview';

// savedJSON is application-owned checkpoint data; choose the alert explicitly.
const checkpoint = validateEventDeadlineCheckpoint(savedJSON);
const selected = checkpoint.pending[0] ?? checkpoint.acknowledged.at(-1);
if (!selected) throw new Error('No retained deadline alert to investigate');
const reference = { incidentId: selected.incidentId, sequence: selected.sequence };
const controller = new AbortController();
const result = await investigateEventDeadlineAlert(checkpoint, reference, {
  limit: 25,
  timeoutMs: 5000,
  signal: controller.signal,
});

console.log(result.alert.at, result.page.window_at, result.page.observed_at);
console.log(result.notificationState, result.membership);
for (const row of result.page.recipients) {
  console.log(row.event_source, row.event_id, row.accepted_at,
    row.deadline_at, row.waiting_reason, row.expired);
}
if (result.page.has_more) {
  const next = await investigateEventDeadlineAlert(checkpoint, reference, {
    limit: 25,
    cursor: result.page.next_cursor,
    signal: controller.signal,
  });
  // Inspect next explicitly; the helper does not accumulate pages.
  console.log(next.page);
}
```

The helper revalidates and detaches the checkpoint on every call. It accepts Node
version 1 count checkpoints and version 2 ratio checkpoints, using the same policy,
sample, incident lineage and notification validation as checkpoint loading. An
exact match must exist in `pending` or the retained `acknowledged` history. Unknown,
mismatched and evicted references reject before HTTP. `opened` and `resolved`
alerts are supported; incident state need not still be firing.

Validation does not authenticate the saved checkpoint or reprove its original
sustained confirmation. Every request independently enforces server authorization,
scope and MFA. Go/CLI and Python checkpoint formats remain separate.

## Selection and returned metadata

Filters come from the validated checkpoint's target and matching policy rule.
Callers cannot override the application, subscription, horizon, status or reason.

| Selected rule | Current triage query |
| --- | --- |
| Upcoming total count | `status=upcoming`, `withinMillis=policy.withinMs` |
| Upcoming reason count | Same horizon plus `waitingReason=rule.reason` |
| Expired total count | `status=expired`, no relative horizon |
| Expired reason count | Expired rows plus `waitingReason=rule.reason` |
| Expired ratio | Expired rows, no horizon or reason filter |

Ratio investigation lists the expired numerator only. It does not list the
denominator, recompute the ratio or check its minimum qualifying cohort. The
returned `alert.ratioSample`, when present, is the saved historical sample. A fresh
complete deadline-window read is required for current aggregate facts.

The result includes detached `target`, `alert`, `query` and validated `page` fields.
`notificationState` describes whether the supplied checkpoint retained the alert
as `pending` or `acknowledged`; it says nothing about delivery state. `readOnly`
is true and `membership` is `current_at_triage_observation`.

`alert.at` remains the saved notification time and is never a deadline filter.
`page.window_at` is the first page's classification window; `page.observed_at`
is the current page observation. These timestamps describe different facts.
Recipients have current retained pending state and observed waiting reasons;
the helper does not reconstruct the original incident's membership.

## Bounds, cancellation and continuation

`limit` defaults to 50 and requires an integer 1..100; explicit zero and null are
invalid. `cursor` is opaque, at most 4096 UTF-8 bytes; omitted or empty starts a
new traversal. `timeoutMs` defaults to 5000 and requires whole milliseconds
100..5000. Invalid options reject before HTTP. Options and checkpoint data are
captured before the first await, so later caller changes cannot alter the request.

Each invocation makes one logical generated listing call. The existing transport
may retry within the request budget. Timeout cancels the underlying request and
rejects with a `DOMException` named `TimeoutError`. Caller cancellation propagates
the signal's reason; a pre-aborted signal prevents the request. API and transport
errors propagate without a partial investigation. Timers and abort listeners are
removed when the read settles.

Continue using the same checkpoint target, selected alert and limit with the full
`page.next_cursor`. Do not decode the cursor or silently restart on an error. The
server binds it to authenticated scope, normalized filters, page size and the
first-page window, with a 15-minute lifetime. It is not bound to an alert identity,
does not grant access and does not reserve work. The helper makes no automatic
continuation requests or delivery actions.

Status selection uses the fixed classification window. The upcoming interval is
strictly after that window and strictly before window plus horizon. A later
upcoming page can contain `expired: true` when its observation advances past a
deadline. Waiting reasons and membership remain live: rows can move, disappear,
be skipped or recur. `has_more` reflects lookahead at one observation and does not
guarantee a nonempty next page. Empty or final pages do not establish recovery or
successful business completion.

## Response validation

The helper rejects incorrect read-only coverage/scope, malformed or regressing
page clocks, too many rows, contradictory cursor metadata, incomplete nonfinal
pages and self-repeating cursors. It requires valid identities and RFC3339
timestamps, known waiting reasons, matching cohort/reason filters, correct expiry
flags and nondecreasing visible deadline/acceptance sort keys. Nanosecond ordering
is preserved. The internal outbox tie-breaker is not exposed for verification.

Only known metadata fields are reconstructed in the result; unknown response and
checkpoint fields are discarded. Recipients contain identities, deadlines,
waiting reasons and optional scheduling timestamps, without event payloads.
The page-size bound does not bound database rows scanned or response field sizes.

The helper performs no acknowledgment, checkpoint update, notification, retry,
replay or control mutation. See [Node watch semantics](event-deadline-node-watch.md),
[triage coverage and retention](event-subscription-deadline-triage.md) and the
separate [Go/CLI investigation helper](event-deadline-alert-investigation.md).
Python consumers have a separate
[`ainvestigate_event_deadline_alert`](event-deadline-python-investigation.md) helper
for Python checkpoints with the same current-membership semantics.
