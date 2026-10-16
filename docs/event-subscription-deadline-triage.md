# Investigate pending delivery deadlines

`GET /v1/apps/{slug}/event-subscriptions/{subscriptionID}/delivery-control/deadlines`
returns a read-only, payload-free page of retained captured pending application
deliveries with effective deadlines. Use it after a
[full pending-summary alert](event-subscription-pending-summary.md) to identify
specific event source/ID pairs that need investigation. It requires the existing
read scope and MFA policy and returns `Cache-Control: no-store`.

Each row contains event source/ID, acceptance time, effective deadline, an expiry
flag, observed waiting reason, and optional next-attempt and lease timestamps. The
response identifies the application and subscription once for the entire page.
Waiting reasons reuse the backlog's control, capacity, lease and retry precedence.
They describe an observation, not proof of a unique root cause or a promise that
an apparently ready delivery will run next. Expiry is reported separately; an
expired row can still have the backlog waiting reason `ready`.

## Filters and ordering

| Parameter | Meaning |
| --- | --- |
| `deadline_from` | Inclusive absolute effective-deadline bound |
| `deadline_before` | Exclusive absolute effective-deadline bound |
| `status` | `all` (default), `expired`, or `upcoming` |
| `limit` | 1..100 recipients; default 50 |
| `cursor` | Opaque continuation returned by the previous page |
| `within_millis` | Optional relative upcoming horizon, 100..86400000 ms, anchored to the first page |
| `waiting_reason` | Optional known observed backlog reason, applied before pagination |

Time bounds accept RFC3339 timestamps and normalize to UTC microseconds. When both
are present, the normalized lower bound must precede the upper bound. Unknown or
repeated query parameters are rejected. Optional filter omission and its documented
default are equivalent. A relative window normalizes omitted/`all` status to
`upcoming`; combining it with `expired` is rejected. Its upper boundary is exclusive.
Waiting reasons remain live on each page. Preserve both new filters across pages.

Rows sort by effective deadline ascending, then acceptance time ascending, then
internal receipt identity ascending. Tie-breakers allow continuation even if the
boundary row has since been delivered or removed. This diagnostic order is not a
scheduler or consumer execution-order guarantee.

Only captured pending application recipients with effective delivery-age deadlines
are included. The same captured retry policy and replay override rules used by
admission and rate previews apply. Processing recipients, publications awaiting
recipient capture, recipients without deadlines, workflow recipients and purged
history are excluded. Ordered-work and delivery-age overrides that suppress an
effective deadline do not acquire one for this listing. Expired pending rows remain
visible until their routing state changes.

## Live pages and time

The first page establishes `window_at`. All pages restrict acceptance time to that
boundary, and `status` filters compare effective deadlines with that same fixed
window (`expired` means deadline <= window; `upcoming` means deadline > window).
This prevents the expiry filter from sliding forward during a traversal.

Each page uses a fresh `observed_at` and current routing state and waiting reasons
from one read-only Repeatable Read snapshot. The per-row `expired` flag compares
the deadline with this fresh observation. An `upcoming` row can therefore have
`expired: true` on a later page if its deadline has elapsed since `window_at`.
Refresh the traversal to change the classification window.

This is **live pagination**, not frozen membership. Deliveries can complete,
expire, be replayed or lose their deadline between reads. Backfill can capture an
older receipt after the first page. Rows can appear, disappear or move across the
cursor boundary and can be skipped or revisited when their deadline changes.
`has_more` means a lookahead row existed in the current page snapshot; the next
page can still be empty. No cursor reserves work or extends retention.

The cursor is bound to account, application, subscription, normalized filters and
page size. Preserve those values across pages. It expires 15 minutes after the
initial window, rather than being extended on each page. Invalid, expired or
mismatched cursors return 400; restart from the first page. Cursors are navigational
metadata, carry no authorization, and must remain opaque to callers. Ownership,
read scope and MFA are checked independently on every request.

## Budgets and SDKs

Reads have a five-second context budget and transaction-local four-second database
statement timeout. The database returns at most limit + 1 metadata rows. The
in-memory implementation retains at most that many candidate rows. Finding and
sorting the earliest matching deadlines can still scan a large scoped backlog;
the response bound is not a bound on rows examined or a performance guarantee.
Narrow the absolute deadline bounds when needed. Direct store reads use the same
request budget. Timeouts return 504 without partial pages. Invalid selected
metadata fails the read rather than fabricating a delivery identity or timestamp.

Go clients expose `ListEventSubscriptionDeadlines` with
`EventSubscriptionDeadlineQuery`. Node and Python generated clients expose the
same operation. For example:

```go
before := time.Now().UTC().Add(5 * time.Minute)
query := faas.EventSubscriptionDeadlineQuery{
    DeadlineBefore: &before,
    Status: "upcoming",
    Limit: 50,
}
page, err := client.ListEventSubscriptionDeadlines(ctx, app, subscriptionID, query)
// Handle err before using the page. Preserve before, status and limit on continuation.
if err == nil && page.HasMore {
    query.Cursor = page.NextCursor
    page, err = client.ListEventSubscriptionDeadlines(ctx, app, subscriptionID, query)
}
```

An empty page does not prove successful delivery or deadline safety. Use the
[full summary](event-subscription-pending-summary.md) for aggregate coverage and
[the rate preview](event-subscription-rate-preview.md) for bounded advisory admission
modeling. This listing performs no retry, replay, acknowledgement, recovery,
configuration mutation or production DDL. No migration is required.

For complete upcoming counts and separate expired waiting-reason partitions, use
[deadline-window monitoring](event-subscription-deadline-window.md). It adds optional
sustained count alerts and matching relative-window/reason triage filters.

## Investigate a supplied Go alert

`InvestigateEventDeadlineAlert` selects the matching current upcoming/expired cohort
and optional waiting reason from a Go/CLI deadline count or ratio alert. It performs
one bounded page read and validates scope/cohort metadata. The original notification
reference is separate from triage observation clocks; historical membership is not
reconstructed. See [usage, pagination and limits](event-deadline-alert-investigation.md).

The CLI command `events deadline-alert-investigate APP --alert-file PATH` exposes
this helper with `--limit`, `--cursor`, a human table and global `--json` output.
It accepts one saved Go/CLI alert object and preserves full continuation tokens.
See [CLI investigation](event-deadline-alert-investigation.md#cli-investigation).

Add `--explain` (Go option `Explain: true`) with a page limit at most 50 to read
one acceptance-guarded batch of current blocker diagnostics. Per-row diagnostic
clocks and missing/error states remain separate from triage observations. See
[blocker explanations](event-deadline-alert-investigation.md#blocker-explanations).

Node consumers can use `investigateEventDeadlineAlert(checkpoint, reference, options)`
with an exact retained incident/sequence pair. It derives the query from validated
checkpoint policy and returns one current page with separate notification and
observation clocks. See [Node investigation](event-deadline-node-investigation.md).

Python consumers can use `ainvestigate_event_deadline_alert(client, checkpoint, reference, options)`
with a retained Python alert. It preserves nanosecond validation, current membership
and bounded continuation with task cancellation. See
[Python investigation](event-deadline-python-investigation.md).
