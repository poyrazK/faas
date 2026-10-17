# Investigate a Go deadline alert with current triage

`InvestigateEventDeadlineAlert` maps a supplied Go delivery-health alert to one
bounded page from the existing read-only deadline triage endpoint. It returns
current retained pending delivery identities, effective deadlines, observed waiting
reasons, scheduling metadata and continuation details. It does not fetch payloads,
retry/replay/cancel work, acknowledge notifications, update checkpoints or send
messages.

Set `EventDeadlineInvestigationOptions.Explain=true`, or use CLI `--explain`, to
read optional acceptance-guarded [blocker explanations](#blocker-explanations) for
the selected page.

```go
// alert is an EventDeliveryHealthAlert loaded from a saved Go/CLI alert stream.
result, err := client.InvestigateEventDeadlineAlert(ctx, "orders", alert,
    faas.EventDeadlineInvestigationOptions{Limit: 25})
if err != nil {
    return err
}
fmt.Println(result.Membership) // current_at_triage_observation
fmt.Println(result.Alert.AlertAt, result.Page.WindowAt, result.Page.ObservedAt)
for _, row := range result.Page.Recipients {
    fmt.Println(row.EventSource, row.EventID, row.AcceptedAt,
        row.DeadlineAt, row.WaitingReason, row.Expired)
}
if result.Page.HasMore {
    next, err := client.InvestigateEventDeadlineAlert(ctx, "orders", alert,
        faas.EventDeadlineInvestigationOptions{
            Limit: 25,
            Cursor: result.Page.NextCursor,
        })
    // Handle err and next before continuing; there is no automatic page loop.
    _, _ = next, err
}
```

The helper is available on the root API client and standalone Go SDK, whose public
surface aliases the options, alert reference and investigation result types. It
supports Go/CLI `EventDeliveryHealthAlert` objects, not Node/Python checkpoint alerts.
No new server route, API schema or database migration is required.

Node consumers have a separate
[`investigateEventDeadlineAlert`](event-deadline-node-investigation.md) helper that
selects an exact retained incident/sequence pair from a validated Node checkpoint.
It preserves the same current-membership distinction using Node's checkpoint format.

Python consumers can use
[`ainvestigate_event_deadline_alert`](event-deadline-python-investigation.md) with a
validated Python checkpoint and exact retained incident/sequence pair. It preserves
nanosecond timestamps and supports task cancellation and opaque continuation.

## Selection from the metric

| Alert metric | Current triage selection |
| --- | --- |
| `deadline_window.upcoming_recipients.<millis>` | `status=upcoming`, `within_millis=<millis>` |
| Upcoming count with `.reason.<label>` | Same upcoming window plus `waiting_reason=<label>` |
| `deadline_window.expired_recipients.<millis>` | `status=expired`, with no relative horizon |
| Expired count with `.reason.<label>` | All expired rows plus `waiting_reason=<label>` |
| `deadline_window.expired_ratio_basis_points.<millis>.minimum_recipients.<N>` | All expired rows, representing the ratio numerator |

Metric parsing requires the same canonical horizon, known reason and valid minimum
cohort syntax as the Go watch. Unsupported/malformed metrics, including modeled
preview risk and earliest-deadline boolean metrics, are rejected before HTTP. Count
thresholds do not limit the requested sample; the caller's page size does.

Expired queries deliberately have no `within_millis`: the expired cohort covers
all expired deadlines regardless of the alert horizon. Ratio investigations do not
list the denominator, recompute the ratio or establish whether its minimum cohort
still qualifies. Use a new complete deadline-window read for those aggregate facts.

## Alert reference and scope

The supplied app slug is authorized normally by the existing endpoint. The helper
requires nonzero application/subscription UUIDs, a valid notification timestamp,
bounded sequence and canonical Go incident identity. `opened`, `resolved` and
`confirmation_lost` references are accepted with their appropriate opening/closing
sequence relationship. The returned application/subscription must match the alert
scope before any rows are exposed.

The result retains a small `Alert` reference: metric, canonical application and
subscription IDs, incident ID, sequence, transition and `AlertAt`. This validates
selection/correlation metadata, not the original watch's timing or count proof.
It is not a signed alert authenticity check. Every read independently enforces
ownership, scope and MFA; supplying an alert grants no access.

## Current observations, not historical membership

`Membership="current_at_triage_observation"` and `ReadOnly=true` describe the result.
`Alert.AlertAt` is the supplied alert notification time, **not** the original count
snapshot timestamp. It is not used as a filter. The first triage page establishes
a fresh `Page.WindowAt`; each page has its own `Page.ObservedAt` and current pending
state/waiting reasons. No reconstructed list of the original incident's recipients
is claimed, and even the first page may be empty after a valid opening alert.

The relative upcoming interval is strictly after the traversal's window and strictly
before that window plus its horizon. Expired filtering compares against the same
fixed window. On a later upcoming page, `row.Expired` can therefore be true at the
new observation, even though it matched the traversal's upcoming interval. The
helper preserves this existing distinction rather than dropping such rows.

Membership can change after completion, capture/backfill, expiry, replay, age
overrides, reason changes or retention. A page cannot confirm whether an alert is
still firing or successfully recovered. Returned identities include acceptance time
for correlation; the helper does not deduplicate incarnations or authorize a later
mutation against them.

## Bounds, pagination and validation

`Limit` defaults to 50 and accepts 1..100; zero selects the default. Explanation
mode requires 1..50. Each invocation makes at most one GET, plus an optional blocker
batch, with the existing shared five-second read context budget, shortened
by the caller's deadline/cancellation. Errors return no partial investigation.
The HTTP transport may retain its normal retry policy within that context. A row
bound does not bound database rows scanned or response field byte sizes.

`Query` records the exact SDK query used. Continue with the same app, alert and
limit plus `Page.NextCursor`; do not add manual filters. Cursors stay opaque and
are bound by the server to authenticated scope, normalized filters, page size and
first-page classification window. They expire 15 minutes after that window.
They are not bound to an alert identity and do not grant access or reserve work.
Invalid/expired cursors propagate their API error; the helper does not silently
restart a traversal.

The helper rejects missing read-only coverage, mismatched scope, invalid or
regressing page clocks, excessive rows, contradictory cursor/has-more metadata,
self-repeating cursors, unknown waiting reasons, malformed identities/timestamps,
incorrect expiry flags, rows outside the chosen cohort/reason and unordered visible
sort keys. It preserves server order rather than sorting or discarding invalid rows.
The visible ordering check covers deadline and acceptance time; the server's
internal outbox tie-breaker is not exposed for independent verification.

Pagination remains live. Rows can appear, disappear, move, be skipped or recur when
deadlines/routing change. `HasMore` reflects lookahead in that page's snapshot and
does not guarantee a nonempty next page. An empty final page does not prove delivery
success. No cursor decoding, payload reads or automatic multi-page accumulation
are performed. See [triage coverage and retention semantics](event-subscription-deadline-triage.md).

## CLI investigation

```sh
gregale events deadline-alert-investigate orders \
  --alert-file ./deadline-alert.json --limit 25

gregale --json events deadline-alert-investigate orders \
  --alert-file ./deadline-alert.json --limit 25
```

The file must contain **one Go/CLI lifecycle alert JSON object**, for example one
separately saved `--alerts-json` line or one entry extracted from a Go checkpoint's
`pending`/acknowledged alerts. A complete checkpoint, JSON array, entire JSON-lines
stream, investigation result or Node/Python alert is not accepted. No automatic
selection among several incidents occurs.

`--alert-file` is required and must name a regular local file of at most 64 KiB.
Input uses the CLI's existing protected file opener; symlinks and non-regular inputs
are rejected. Duplicate or case-colliding keys, unknown/case-aliased/null fields,
missing reference fields, malformed JSON and multiple JSON values are rejected.
Recognized Go alert fields are decoded without modifying the file. Required reference
fields are `metric`, `app_id`, `subscription_id`, `incident_id`, `sequence`,
`transition` and `at`. The SDK validates supported metrics, identity and transition
sequence before making the triage request. This does not authenticate the original
alert or reprove its watch timing.

`--limit` defaults to 50 and accepts 1..100. `--cursor` accepts the previous page's
opaque `next_cursor`, with the existing 4096-byte bound. Each command performs one
logical page read with the Go helper's five-second budget; Ctrl-C cancels it.
Authentication, scope and MFA remain the existing endpoint's responsibility.
A bad or expired cursor produces an error; the command does not restart pagination.

Text output separates alert notification time, traversal classification window and
current page observation. It lists event source/ID, acceptance and deadline times,
current expiry flag, waiting reason, next attempt and lease timestamp. Sources and
IDs are printed in full as quoted strings so control characters cannot change the
table layout. An empty page explicitly does not certify delivery success. The text
labels membership as current, rather than reconstructing the original incident.

When `Has more` is true, text output prints the **full quoted cursor**, without
abbreviation. Continue with the same app, file and limit:

```sh
gregale events deadline-alert-investigate orders \
  --alert-file ./deadline-alert.json --limit 25 --cursor 'OPAQUE_NEXT_CURSOR'
```

Global `--json` returns the full Go investigation result. Use `page.next_cursor`
for continuation, `page.has_more` for lookahead, `alert.alert_at` for notification
time, and `page.window_at` / `page.observed_at` for the independent triage clocks.
No input alert, acknowledgment inbox or checkpoint is changed. The command neither
accumulates all pages nor performs recovery actions. Existing live membership,
ratio-numerator-only and later-page expiry semantics above continue to apply.

## Blocker explanations

```sh
gregale events deadline-alert-investigate orders \
  --alert-file ./deadline-alert.json --limit 25 --explain

gregale --json events deadline-alert-investigate orders \
  --alert-file ./deadline-alert.json --limit 25 --explain --cursor 'OPAQUE_NEXT_CURSOR'
```

Go callers set `EventDeadlineInvestigationOptions{Limit: 25, Explain: true}`.
The root API client and standalone Go SDK expose the same option and explanation
types. Node and Python retain their existing single-page investigation helpers;
this option belongs to the Go/CLI workflow.

Explanation mode requires a normalized page limit of at most 50, validated before
HTTP. Defaults remain 50; explicit SDK zero selects that default. The helper first
validates the normal triage page, then submits at most one read-only
`POST /v1/events/recipient-blockers` batch. Each query carries the exact source,
event ID, subscription and `expected_accepted_at` from that triage row. The batch
endpoint independently enforces its existing authorization; the page grants no
additional access. An empty page makes no batch request.

The GET and optional POST share the existing five-second context budget, shortened
by caller cancellation/deadline. Normal transport retries remain within it. The
helper neither walks additional pages nor follows ordering-blocker chains.
Whole-request transport/API errors and cancellation return no investigation.

The optional `explanations` object contains `coverage="selected_triage_page"`,
`attempted`, `read_complete` and one item per triage row in the same order.
`attempted` counts queries submitted in the batch, not successful reads or database
rows scanned. Each item preserves its requested acceptance guard and distinguishes:

| Status | Meaning |
| --- | --- |
| `available` | Valid current pending/processing routing evidence |
| `not_waiting` | Routing is now enqueued, filtered or failed; handler success is not established |
| `unavailable` | Receipt, capture, recipient or routing evidence is unavailable |
| `acceptance_replaced` | The retained source/ID belongs to a different acceptance; no replacement evidence is returned |
| `read_error` | An individual lookup failed/timed out, or returned invalid/mismatched evidence |
| `unsupported_recipient` | The row cannot fit the existing blocker query bounds |
| `not_read` | Adding the query would exceed the existing 64 KiB batch body budget |

The blocker endpoint limits identity fields to 256 bytes. Longer triage identities
remain in the original page and receive `unsupported_recipient/blocker_query_bounds`.
Request sizing uses the actual JSON encoding, including escaping. Rows that do not
fit are marked `not_read/request_body_budget`, while later rows may still fit.
The helper never splits these into additional batches or silently drops them.

`read_complete` is false for any failed, invalid, unsupported or unread row. It is
true when every row received a valid scoped response, including valid unavailable
or replaced-acceptance responses. It describes reads only, never complete usable
evidence, an exhaustive backlog or successful delivery. An empty page has no items,
zero attempts and `read_complete=true`, without establishing recovery.

Valid items retain their own `observed_at`. Batch items are independent snapshots,
separate from `page.observed_at`, `page.window_at` and the saved alert notification
time. Routing state, waiting reason and scheduling metadata can change after the
triage read. The helper does not require the new reason to match the earlier page
filter or claim that both reads describe one atomic observation. Regressing
diagnostic timestamps are rejected as invalid evidence.

Available routing projections contain the observed waiting reason, target
availability, capacity scope, next retry and lease timestamps, plus optional
ordering-blocker identity, state and retry time. The ordering blocker can belong
to another subscription in the same lane. The result omits payloads, resolved
work keys, server URLs and unrelated fields. These labels describe observed
constraints and do not prove a unique cause or authorize recovery actions.

Batch query echoes, result identities, application scope, acceptance timestamps,
status/reason combinations, waiting reasons, capacity scope, ordering metadata and
scheduling timestamps must validate before projection. Missing/extra batch items
invalidate correlation for all submitted rows. In a matching-length batch, reordered
or mismatched echoes, simultaneous result/error values and malformed evidence produce
errors for the affected rows while other valid items remain available. Unvalidated
recipient metadata is never returned. Error rows have no diagnostic timestamp or routing
projection; the triage observation is never substituted as diagnostic evidence.

CLI text prints the independent diagnostic clocks and full quoted identities,
including ordering-blocker subscription IDs. Global JSON includes the same optional
explanation structure. If `read_complete=false`, the CLI writes the result and exits
1 so scripts can inspect each item; valid unavailable/replaced evidence alone does
not cause an incomplete-read exit. A normal request failure writes the command error.
Preserve app, alert and limit for cursor continuation; repeat `--explain` to explain
that next page too. No subscription control or delivery is changed.
