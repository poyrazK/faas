# Investigate a Python deadline alert

`ainvestigate_event_deadline_alert(client, checkpoint, reference, options)` from
`faas_sdk` returns one current read-only triage page for an exact retained Python
alert. Use the authenticated generated client, normally `client.inner` from a
configured `FaaSClient`, in its owning event loop.

```python
from faas_sdk import (
    EventDeadlineInvestigationOptions,
    ainvestigate_event_deadline_alert,
    validate_event_deadline_checkpoint,
)

# saved_json is application-owned checkpoint data; select a retained alert.
checkpoint = validate_event_deadline_checkpoint(saved_json)
selected = next(iter(checkpoint["pending"]), None)
if selected is None:
    selected = next(iter(reversed(checkpoint["acknowledged"])), None)
if selected is None:
    raise ValueError("No retained deadline alert to investigate")
reference = {"incident_id": selected["incident_id"], "sequence": selected["sequence"]}
result = await ainvestigate_event_deadline_alert(
    client.inner,
    checkpoint,
    reference,
    EventDeadlineInvestigationOptions(limit=25, timeout_ms=5000),
)

print(result["alert"]["at"], result["page"]["window_at"], result["page"]["observed_at"])
print(result["notification_state"], result["membership"])
for row in result["page"]["recipients"]:
    print(row["event_source"], row["event_id"], row["accepted_at"],
          row["deadline_at"], row["waiting_reason"], row["expired"])
if result["page"]["has_more"]:
    following = await ainvestigate_event_deadline_alert(
        client.inner,
        checkpoint,
        reference,
        EventDeadlineInvestigationOptions(limit=25, cursor=result["page"]["next_cursor"]),
    )
    # Inspect this page explicitly; no automatic accumulation occurs.
    print(following["page"])
```

Every invocation revalidates and detaches the complete Python checkpoint. Both
version 1 count and version 2 ratio checkpoints are supported with existing policy,
sample, incident lineage and notification validation. The exact incident/sequence
pair must occur in `pending` or retained `acknowledged` history. Unknown, mismatched
or evicted references reject before HTTP. Both opening and resolution alerts can
be investigated, independently of the incident's current firing state.

This validation does not authenticate checkpoint contents or reprove the original
sustained confirmation. The endpoint independently enforces authorization, scope
and MFA on every read. Go/CLI and Node checkpoint formats remain separate.

## Selection and observations

The helper derives application, subscription and filters from the validated target
and matching rule. Callers cannot override these selections.

| Selected rule | Current triage query |
| --- | --- |
| Upcoming total count | `status=upcoming`, `within_millis=policy.within_ms` |
| Upcoming reason count | Same horizon plus `waiting_reason=rule.reason` |
| Expired total count | `status=expired`, no relative horizon |
| Expired reason count | Expired rows plus `waiting_reason=rule.reason` |
| Expired ratio | Expired rows, no horizon or reason filter |

Ratio investigation covers the expired numerator only. It neither lists the
denominator nor recomputes a ratio or its qualifying minimum cohort. The saved
`alert.ratio_sample` remains historical. Read the complete deadline window again
for current aggregate facts.

The returned dictionary contains detached `target`, `alert`, `query` and validated
`page` metadata. `notification_state` describes retention in the supplied checkpoint
as `pending` or `acknowledged`, without asserting delivery state. `read_only` is true
and `membership` is `current_at_triage_observation`.

`alert.at` is the saved notification time and is never a deadline filter.
`page.window_at` establishes the traversal's classification window, while
`page.observed_at` describes the current page observation. Recipients and reasons
reflect current retained pending work. They do not reconstruct original incident
membership. Timestamps remain original RFC3339 strings; validation uses integer
nanoseconds from raw response JSON, preserving ordering beyond generated model
datetime microseconds.

## Bounds, cancellation and errors

Omitting options, or passing `None`, uses `EventDeadlineInvestigationOptions()`.
`limit` defaults to 50 and requires an integer 1..100. `cursor` defaults to the empty
string and requires a UTF-8 string of at most 4096 bytes; empty starts a new traversal.
`timeout_ms` defaults to 5000 and requires integer milliseconds 100..5000. Booleans,
floats, null option fields and invalid bounds reject before HTTP. Checkpoint,
reference and option values are captured before the first await.

Each invocation makes one logical generated listing call. Existing transport retries
remain within the helper's `asyncio.timeout` budget. Timeout raises `TimeoutError`;
normal task cancellation propagates `asyncio.CancelledError` to the in-flight read.
There are no detached background requests. Cancellation follows asyncio's
cooperative cleanup behavior and does not cancel durable delivery work.

Existing API, transport, generated parsing and JSON errors propagate. A non-200
response returned by the generated client raises `EventDeadlineInvestigationReadError`
with `status_code` and `retry_after`. Invalid page metadata raises `ValueError`.
Errors, timeout and cancellation return no partial investigation, and the helper
does not acknowledge alerts or change checkpoints.

## Continuation and response validation

Continue with the same checkpoint target, selected alert and limit plus the full
`page.next_cursor`. The server binds opaque cursors to authenticated scope, filters,
page size and the first-page window; they expire after 15 minutes. Cursors are not
bound to an alert identity, grant no access and reserve no work. Invalid or expired
cursors produce an error; no silent restart, decoding or multi-page loop is added.

The helper validates read-only coverage, matching application/subscription UUIDs,
page clocks, page size and consistent cursor/has-more metadata. It rejects
self-repeating cursors, incomplete nonfinal pages, malformed identities/timestamps,
unknown reasons, incorrect expiry flags, rows outside the selected cohort/reason
and regressing visible deadline/acceptance sort keys. It preserves server order;
the internal outbox tie-breaker is not exposed for independent verification. Only
known page and recipient fields are reconstructed, discarding unknown fields.

Upcoming status is strictly after the fixed window and strictly before that window
plus its horizon. A later upcoming page can have `expired: true` at its newer
observation. Reasons and membership remain live: rows can appear, disappear, move,
be skipped or recur. `has_more` reflects one page's lookahead and does not guarantee
a nonempty following page. An empty or final page cannot establish successful
delivery or recovery. Page size does not bound scanned database rows or response
field sizes.

Investigation reads no event payloads and performs no retry, replay, notification,
acknowledgment or control mutation. See [Python watch semantics](event-deadline-python-watch.md),
[triage coverage and retention](event-subscription-deadline-triage.md),
[Go/CLI investigation](event-deadline-alert-investigation.md) and
[Node investigation](event-deadline-node-investigation.md).
