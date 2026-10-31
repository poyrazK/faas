# Events and subscriptions

Publish application events, subscribe workflows and consumers to them, and
recover failed deliveries. Events are a preview; this page moved out of
[event-driven workloads](event-driven.md) so that page can be published
(#5709), and it is published when events reach beta.

## Event workflow starts

A workflow can start directly from an internal event without an adapter handler:

```yaml
workflows:
  - name: paid_invoice
    trigger:
      type: event
      source: billing.*
      event_type: invoice.paid
      filter:
        data:
          amount:
            $gt: 100
      enabled: true
    steps:
      - name: record
        path: /record-payment
        input:
          invoice_id: "{{input.data.invoice_id}}"
      - name: notify
        path: /send-receipt
        depends_on: [record]
        input:
          receipt_id: "{{steps.record.output.receipt_id}}"
```

The required source and event type accept the same exact and edge wildcard
patterns as subscriptions. The optional filter is a YAML/JSON object evaluated
against the CloudEvents envelope. A matching run receives that full envelope
as input, so `{{input.data.invoice_id}}` selects the producer's invoice ID.
Event triggers reject schedule, timezone, fixed input, and overlap fields.
Several events can start concurrent runs, subject to the existing app run quota.
A run waiting on an event (`awaiting_event`) does not count toward that quota
until the app has more parked runs than its plan's parked allowance: 100, 1,000
and 10,000 per app on Hobby, Pro and Scale (ADR-20261023215834). Parked runs
beyond the allowance count as concurrent. A run woken by its event returns to
`pending` and counts again, even when that takes the app over its quota; new
runs are refused until the app is back under it. An event that would start a
run while the app is at its quota waits like a full delivery capacity (scope
`app`): it is retried every few seconds without spending its routing attempts,
and its configured delivery age limit still applies.

Use `gregale event preview` before publishing; its samples include the workflow
name, deployment ID, and stable recipient ID. Preview checks matching intent and
does not reserve capacity or guarantee runtime availability. Publish with a
stable source and `--id` when retrying the same logical event. Find admitted runs
through `gregale workflow list --app APP`, then use workflow status, attempt,
and cancellation commands as usual. Ordinary subscription invocations remain
visible through `gregale event deliveries APP`.

Event starts are part of the workflow preview on Hobby and above. Enable
`FAAS_WORKFLOWS_ENABLED=1` on apid and schedd and configure the gateway executor.
Only the preferred live default deployment contributes workflow candidates;
preview deployments, maintenance apps, inactive or held accounts, and apps
requiring platform tenant context do not capture new recipients. Apply the
migration and upgrade schedulers before deploying these manifests. Older
schedulers cannot interpret workflow recipients safely. Drain accepted workflow
receipts and finish or cancel their runs before rolling back binaries.

At publish time Gregale captures matching source/type candidates with their
workflow definitions and filters. Redeploying, disabling, or removing a trigger
changes future events; already accepted events keep the original definition.
Step handlers run against the app's serving deployment. No historical events
are backfilled. Identical event identities retain the original recipients.
Admission commits a run with a durable event receipt, preventing duplicate runs
on recovery even after run history is pruned. Event identities and receipts use
the plan's [settled-receipt window](#settled-receipt-retention).

Independent recipient routing also covers workflow-only and mixed
workflow/application events. Workflow admission and its routing checkpoint
commit together under the recipient's lease. A blocked workflow does not make
a failed sibling wait for the parent receipt to settle before selective recovery.

Capacity or temporary target failures retry through the existing fanout system.
After the 12-attempt cap, inspect routing failures and replay them using
`gregale event deliveries APP` and `gregale event replay`; `subscription_id`
also identifies workflow recipients. You can select the same routing recovery
with `gregale event recover --source SOURCE --id EVENT_ID --subscription ID`
and preview it with `--dry-run`. After admission, inspect and recover execution
through the workflow run and step commands. Disabling the workflow runtime
leaves workflow recipients waiting without consuming new retry attempts. App
handler side effects must remain idempotent because workflow steps may retry.

See the [two-step event workflow recipe](../examples/event-workflows/README.md)
and [ADR-432](adr/432-event-workflow-starts.md). Independent workflow admission
and its upgrade requirements are recorded in
[ADR-648](adr/648-independent-workflow-event-routing.md).

## Internal event subscriptions

Applications can subscribe to events published through Gregale's internal
event router. A YAML deployment declares subscriptions with `event_triggers`:

For a first worker, scaffold the complete example instead of writing the
manifest and handler by hand:

```bash
gregale init --template event-worker --path ./invoice-worker
cd invoice-worker && gregale deploy --name invoice-worker
```

The generated project includes a safe `billing.*` / `invoice.paid` filter and
an acknowledgement handler. Edit `gregale.yaml` when your event vocabulary is
ready.

```yaml
event_triggers:
  - source: billing.*
    type: invoice.paid
    filter: '{"data":{"amount":{"$gt":100}}}'
```

To coordinate related event deliveries, declare a named work policy and bind
the subscription to a scalar field in the CloudEvents envelope:

```yaml
work_policies:
  - name: document-index
    max_running_per_key: 1
    pending_updates: keep_latest
    debounce_ms: 3000
    expires_after_ms: 600000
event_triggers:
  - source: documents
    type: document.edited
    work_policy: document-index
    work_key: data.document_id
```

The same policy name and key can be supplied to an explicit async invocation.
Gregale serializes dispatch within that lane and can replace older pending
work. Running work continues; handlers should still protect external side
effects with an idempotency key or version check. See
[application work policies](work-policies.md) for the complete contract.

To preserve event acceptance order for one key, opt the subscription into
ordered delivery and use a policy that keeps every item until it runs:

```yaml
work_policies:
  - name: ordered-orders
    max_running_per_key: 1
    pending_updates: all
event_triggers:
  - source: orders
    type: order.changed
    work_policy: ordered-orders
    work_key: data.order_id
    ordered: true
```

For an opted-in subscription, Gregale routes the oldest unresolved matching
delivery first for each app, policy, and canonical scalar key. A routing retry
holds that key until it succeeds or reaches a terminal failure; different keys
can proceed independently. After routing admission, the work policy keeps
invocations for that key serialized and in the admitted order. Ordered policies
must set `max_running_per_key: 1`, `pending_updates: all`, and leave debounce
and expiry at zero. These restrictions prevent coalescing, expiry, and parallel
execution from violating the guarantee.

Manual replay restores routing for a failed delivery, but cannot undo a younger
invocation that was already admitted or completed. Replaying older events after
newer work has advanced can therefore change the observed order. Ordering is
per keyed lane; Gregale does not impose a global order across keys or unrelated
subscriptions.

The filter is a JSON object encoded as a string. `app` is optional for a
single-app deploy and is bound to the target application during source-ref
reconciliation. Event-only projects may use the equivalent TOML form,
`[[triggers.event]]`. Both forms use the same source/type/filter validation.

A delivery whose handler fails is retried with backoff. That covers a handler
that throws (the runner answers 500), any other 5xx, a timeout, and a handler
that answers `408` or `429`. Any other 4xx means the handler refused the
event, so it is not retried but moves straight to the dead-letter queue, where
you can replay it after a fix
([ADR 20261031100000](adr/20261031100000-event-delivery-failures.md)). Retries
are paced by the app's `retry_policy` in `gregale.yaml` when it sets one,
waiting at most 5 minutes between attempts, otherwise 5 seconds
doubling to 5 minutes, with ±20 % jitter. The plan limits the attempts (3 on
Hobby, 10 on Pro, 25 on Scale), so a delivery that keeps failing is
dead-lettered after about 15 seconds on Hobby, 20 minutes on Pro and 95
minutes on Scale ([ADR 20261029190000](adr/20261029190000-event-delivery-retry-backoff.md)).
A delivery with a `work_policy` holds the younger deliveries of its key while
it waits, so it backs off only to 30 seconds and is dead-lettered after about
15 seconds, 4 minutes or 11 minutes.
When a delivery is dead-lettered, inspect and replay it through the
app's unified dead-letter queue (`gregale dlq list <app>`); its origin is shown as
`event_subscription`.

Publish an event from the CLI without learning the CloudEvents envelope. Gregale
generates the stable event id for you:

```bash
gregale event publish billing.stripe invoice.paid \
  --data '{"amount":150}'
```

Use `--id` when a producer is retrying the same logical event and needs to
choose the idempotency key explicitly. The flag-based form remains supported:

```bash
gregale event publish --id evt-123 --source billing.stripe --type invoice.paid \
  --data '{"amount":150}'
```

### Settled receipt retention

Gregale keeps an accepted event's receipt until routing settles, then for the
plan's settled-receipt window. The receipt is what makes a repeated
publication of the same identity a duplicate, and what event recovery, replay
preview and backfill read, so all of them follow the same window
([ADR-20261023215834](adr/20261023215834-event-plan-caps.md)):

| Plan | Settled-receipt window | Retained events | Retained bytes |
|---|---:|---:|---:|
| Free | 1 day | 4,096 | 8 MiB |
| Hobby | 3 days | 131,072 | 256 MiB |
| Pro | 7 days | 1,048,576 | 2 GiB |
| Scale | 14 days | 8,388,608 | 16 GiB |

Retained events and bytes count every customer receipt the account holds,
pending or settled, and a publish over either is refused with `429` until
pruning frees room. Each budget covers the window at a sustained rate of about
0.05, 0.5, 1.7 and 6.9 events per second. `GET /v1/events/storage` reports
usage, the budgets and the window (`limits.settled_retention_seconds`).
Unsettled receipts and receipts that a recovery or backfill holds are kept past
the window.

### Routing failures in the dead-letter queue

A recipient whose routing fails for good (its retries run out, its delivery
age expires, or its target is gone) never gets an invocation, so its failure
lives on the event's receipt: `gregale event deliveries APP` lists it, and
`gregale event replay` or `gregale event recover` route it again while the
receipt is kept. When the receipt is pruned at the end of the settled-receipt
window, each failed recipient moves to the dead-letter queue as an
`event_route` entry, kept for 30 days from the failure like other dead letters
([ADR-20261026163443](adr/20261026163443-event-routing-failures-in-the-dead-letter-queue.md)):

- `payload` is the published event and `error_kind` its `failure_code`;
  `error_detail` names the subscription, the event's source, id and type, when
  it was accepted, the last error, the retry stop reason and `retryable`;
  `retry_count` is the routing attempts. The origin is `event_subscription`, or
  `event_workflow` for a workflow recipient.
- The entry is listed under its app (`gregale dlq list APP`), or only in the
  account view (`GET /v1/account/dlq`) when the app no longer exists.
- It cannot be replayed, because the receipt is gone: replay answers
  `409 dead_letter_not_replayable` and replay-all skips it. To deliver the
  event, publish the payload again. A publication after the receipt is pruned
  is accepted as a new delivery and reaches every subscription that matches it
  now, not only the one that failed. Discard the entry with
  `gregale dlq purge APP EVENT_ID`.

### Retention health and expiry alerts

Inspect retained receipts and storage pressure before scheduling recovery:

```sh
gregale event retention --window 24h --json
gregale event retention --source orders --app order-worker --window 6h --limit 20
```

`GET /v1/events/retention` accepts the same `source`, `app`, `window` and `limit`
query parameters under account read scopes and MFA. The lookahead is a whole-second
Go duration from `1s` through `720h`, default `24h`; samples are capped at 100.
Aggregate counts include all matching receipts. Samples contain identities and
nominal deadlines, ordered by deadline, source and id, with `sample_truncated`
when more matches exist. Payloads and work keys are not returned.

Receipt retention begins when routing settles, not when the producer accepts an
event or a handler finishes. The nominal boundary is settlement plus the plan's [settled-receipt window](#settled-receipt-retention).
Unsettled receipts have no pruning deadline. Settled receipts missing a settlement
timestamp are counted as `unknown_deadline_receipts`; expiry alerts degrade
rather than treating these as healthy zero. Hold counts describe settled receipts.
`eligible_for_pruning` counts
unheld receipts strictly past that boundary. `expiring_receipts` counts unheld
receipts from the observation instant through the lookahead, inclusive.
`held_receipts` and `held_due_receipts` distinguish backfill and recovery holds from upcoming
expiry and overdue cleanup. Holds use the exact pruning predicate: running
backfills pin their account acceptance ranges, and retained completed backfills
with retryable failed items pin those receipts. Running holds take precedence
when a receipt has both reasons; backfill holds take precedence over recovery holds.
`recovery_holds` counts settled receipts primarily held by opted-in pending recovery
items. The three hold counts partition `held_receipts`, counting receipts, not jobs.

Bulk recovery jobs protect pending receipts only when created with
`protect_receipts: true` (`--protect-receipts`). Current holds are observations;
other jobs and backfills can release them before a later recovery. Pruning eligibility does
not mean immediate deletion: batching and locks can delay cleanup. No read
extends retention or triggers pruning or recovery.

Source and app filters apply to receipt counts and samples. App attribution uses
captured or backfilled recipient membership; older receipts without that
attribution remain visible in the account-wide report. The `storage` object and
all utilization percentages always cover the entire account. Maximum utilization
is the larger of count and byte utilization; a plan downgrade can put it above
100%. Filtered receipt bytes can differ from account usage, and platform receipts
with no customer storage charge can still appear in receipt counts.

Create webhook-only alerts through existing app alert rules:

- `event_retention_expiring_receipts` counts unheld app receipts already eligible
  for pruning or expiring within the rule's `5m`, `15m`, `1h`, `6h` or `24h` lookahead.
- `event_storage_utilization_pct` observes the account-wide maximum count/byte
  utilization; use a threshold such as 90 to warn before storage fills.

These metrics require an owned app, omit `event_subscription_id`, and use current
observations rather than historical aggregation. Storage rules attached to different
apps observe the same account pressure. Existing cooldown and recovery webhook
notifications apply; no rules are installed automatically. Read failures produce
degraded observations rather than healthy zeros, and deployment actions are prohibited.

`events recovery-preflight` also reports pending items whose unheld receipt
boundary falls before the later of 24 hours from observation and optimistic drain.
It includes current hold counts, the earliest unheld boundary and a flag when the
rate-only minimum drain reaches or crosses it. Counts refer to pending items,
which may share a receipt. Missing receipts retain their existing classification.
A false crossing flag does not guarantee protection: future waits and changing
holds can still cause receipts to be pruned before recovery.

Apply the retention-health migration before upgrading binaries. Remove the new
alert rules and downgrade binaries before rolling it back. See
[ADR-912](adr/912-event-retention-health.md).

### Pausing events

Gregale can pause the event bus, platform-wide during an incident or for one
account ([ADR-20261027125625](adr/20261027125625-events-kill-switch.md)).
While it is paused:

- Publishing answers `503 events_paused` with `Retry-After`, from
  `POST /v1/events:publish`, `:publish-batch`, the app producer route, the
  platform-tenant route and in-app publishing (the runtime's metadata endpoint
  answers `503 event_publish_unavailable`). Nothing is accepted, so resend the
  same events after the back-off; the SDK outbox does this for you.
- Events already accepted are kept and wait to be routed; routing resumes when
  the pause ends. Delivery-age limits keep counting while they wait.
- Deliveries that already became invocations run as usual.
- Object-storage notifications, inbound webhook automation and other events
  Gregale publishes for you are still accepted and wait with the rest.

### Publish rate

An account publishes at most its plan's rate of events per second: 5 on Free,
20 on Hobby, 100 on Pro and 500 on Scale, with bursts of 100, 200, 1,000 and
5,000 events. Every path counts each event once: `POST /v1/events:publish`,
each event of `POST /v1/events:publish-batch`, `POST
/v1/apps/{slug}/events:publish`, the platform-tenant route and publishes from
an app's runtime. Past the rate a request accepts nothing and answers `429
event_publish_rate_limited` with the limit, the number of events it carried and
`Retry-After`; retry the same events, with the same ids, after that delay. The
rate is kept per API process, and the plan's retained event storage still bounds
the sustained rate. See [ADR 20261026140000](adr/20261026140000-event-app-sources-and-publish-rate.md).

### Batch event publishing

Use `POST /v1/events:publish-batch` to publish 1–100 events in one request
of at most 1 MiB. Send `{"events":[...envelopes...]}` using the same event
attributes, account authentication, MFA and scopes as single-event publication.
Every event must have a stable caller-chosen `id`.

For JSONL imports, put one envelope on each line:

```jsonl
{"id":"order-101","source":"orders","type":"order.created","data":{"order_id":"101"}}
{"id":"order-102","source":"orders","type":"order.created","data":{"order_id":"102"}}
```

```sh
gregale event publish-batch --file orders.jsonl --json
# Or: gregale event publish-batch --file - < orders.jsonl
```

The CLI sends one bounded batch, requires stable ids, and exits nonzero if any
item is rejected or has an unknown outcome. It prints all results before exiting.
Blank JSONL lines are ignored; result indexes refer to event positions, not file
line numbers. It rejects malformed input locally before sending anything.

A structurally valid batch returns HTTP 200 with `results` in input order. Each
result has a zero-based `index`, `status` and `retryable`:

| Status | Meaning | Next action |
| --- | --- | --- |
| `accepted` | New event durably accepted; includes `receipt`. | Inspect the receipt for asynchronous delivery. |
| `duplicate` | Identical identity already accepted; includes the original `receipt`. | No additional fanout or storage charge. |
| `rejected` | Item not accepted; includes `problem`. | Correct invalid content, or retry if `retryable` is true. |
| `unknown` | Acceptance could not be confirmed; includes `problem`. | Retry with exactly the same identity and content. |

Each item commits independently. Invalid attributes, identity conflicts, schema
failures or storage capacity do not roll back accepted siblings. Capacity
problems include the existing limit, observed and retry-after fields. Processing
has a 30-second budget; remaining unattempted items are retryable rejections
with code `event_publish_not_attempted`. Malformed outer JSON, invalid batch
counts and oversized bodies reject the whole request before writes (400 or 413).
Authentication and request rate limiting still apply to the whole request.

Retry an unanswered request, the original file, or only retryable items using
the original source/id/content. There is no request-wide Idempotency-Key replay;
per-event identity supplies deduplication within the plan's settled-receipt
window. Do not generate new ids when retrying. A 200 response does not mean
all events were accepted, and an acceptance receipt does not mean delivery or
handler execution succeeded.

Newly accepted items follow input acceptance order. Duplicates keep their
original position, rejections have none, and concurrent requests may interleave.
Execution ordering still requires opted-in keyed delivery; there is no global
ordering guarantee. Delivery remains at least once. See
[ADR-911](adr/911-batch-event-publication.md).

Gregale identifies an event by account, source, and id. Repeating that
identity with the same type, schema version, and JSON data is safe. Changing
the content returns `409 Conflict` while the plan's [settled-receipt window](#settled-receipt-retention)
keeps the original receipt. Once that receipt is pruned, publishing the same
identity again is a new event: it is delivered again under a delivery id of
its own, which its receipt reports, even while the earlier delivery is still
retained ([ADR 20261026010000](adr/20261026010000-event-reacceptance-delivery-identity.md)). Fanout work is stored with the event, so durable routing resumes after a
scheduler outage. Configured delivery age limits can expire stale recipients. New events capture
their enabled source/type subscription candidates when they are accepted. A
later subscription edit or deletion does not change that event's recipients;
the captured JSON data filter is evaluated when fanout runs. Receipts accepted
before the recipient-snapshot migration continue using the previous routing
behavior, which reads current subscriptions. The scheduler records a routing
outcome for each captured candidate, so a transient enqueue error retries only
that candidate. Retries are capped at 12; terminal routing failures remain on
the outbox receipt, are logged by the scheduler, and move to the dead-letter
queue when the receipt is pruned (see
[Routing failures in the dead-letter queue](#routing-failures-in-the-dead-letter-queue)).
Once an invocation is enqueued, its handler retry and dead-letter lifecycle
applies independently.

Snapshot-backed application routing admits the invocation or work-policy
cancellation and records success in one transaction. An expired routing worker
cannot enqueue, supersede or cancel work. Recovery after an uncertain commit
uses the stored checkpoint, including after delivery rows are pruned; it cannot
admit another delivery or cancel work created later while the receipt is retained.
This applies to both whole-event and independent-recipient routing once every
scheduler is upgraded. Older receipts without snapshots and specialized object
notification destinations keep their existing routing paths. See
[ADR-613](adr/613-atomic-event-routing-handoff.md) for the transaction and upgrade
boundary. Handler side effects still require application deduplication.

Independent recipient routing is enabled by default (ADR-647). Each captured
candidate has its own one-minute lease, renewed while a scheduler is still
routing it (ADR-20261028131500), and backoff from five seconds to five
minutes. A terminal recipient can be replayed while its siblings are routing
or waiting to retry. Each replay gets a fresh twelve-attempt routing budget;
the visible attempt count and history remain cumulative. Successful siblings
are not rerun. On schedd, an unset `FAAS_EVENT_RECIPIENT_CLAIMS_ENABLED` or the
value `1` enables adoption. Set it to `0` on every scheduler before a
mixed-version upgrade; enable adoption after the migrations are applied and
all API and scheduler writers support recipient ownership and atomic admission.
Other nonempty values also disable adoption. Disabling adoption continues
draining already adopted receipts and does not make old binaries safe to restore.
Captured workflows use independent recipient routing too. Upgrade all
schedulers to support atomic workflow recipient admission before enabling
adoption during a mixed-version upgrade. Receipts without snapshots keep
legacy routing. Explicitly disabled adoption keeps whole-event ownership for
new receipts, including those with workflows.
Subscription backfill jobs keep their existing recipient routing under either
flag setting; start new jobs after the fleet upgrade is complete.
See [ADR-647](adr/647-independent-event-routing-default.md) for qualification,
upgrade, and fallback requirements, and
[ADR-606](adr/606-independent-event-recipient-routing.md) for the ownership model.

Publish acceptance means the event is committed on the control plane's
primary database. Its standby replicates asynchronously, so if the primary is
lost in an unplanned failover, events accepted within the standby's
replication lag (normally a few seconds) can be lost. A planned switchover
loses nothing. If you must not lose an event, republish it with the same
identity after a failover: one that survived answers `duplicate`, and one
that was lost is accepted again. See
[ADR 20261029093000](adr/20261029093000-event-ga-gate.md). A recipient marked
`enqueued` has been handled by routing; normally it has an invocation, while
work-policy `cancel_pending` creates a cancellation receipt instead; the receipt is `delivered` when all routing
candidates settle, including terminal failures. Handler completion is tracked
by the invocation lifecycle. Handler execution is at least once: use an
idempotency key or version check for side effects. Subscriptions without
`ordered: true` have no publication-order guarantee. Ordered subscriptions
preserve acceptance order within their keyed lane during normal routing and
automatic retries; explicit replay after newer work has advanced can change
that order.

Find recipients waiting to be routed without knowing their event IDs:

```bash
gregale event backlog
gregale event backlog --app analytics --capacity-scope consumer --min-age 10m
gregale event backlog --consumer-kind workflow --state pending --json
gregale event backlog --origin backfill --subscription-id SUB
```

The API is `GET /v1/events/backlog` with `app`, `subscription_id`,
`consumer_kind`, `origin`, `state`, `capacity_scope` and `min_age_seconds`
filters. It lists captured application and workflow recipients, plus recipients
added by durable backfill, waiting in either routing mode. Rows show event
identity, consumer kind, origin, workflow name when applicable, recorded wait
reason, age since acceptance, cumulative deferrals, retry/lease metadata and
receipt links, plus routing history links for application subscriptions.
Workflow admission and run state are available through the receipt. Consumer
counts cover all matching waiting rows, independently of the recipient page.
This view excludes settled
routing and handler execution queues, which remain available through delivery
inspection.

Use `--after` for the recipient continuation and `--consumers-after` for the
independent consumer continuation; the API names the latter `consumers_after`.
Both pages default to 100 and cap at 200; `--consumer-limit` controls consumer
summaries. Keep filters unchanged and use cursors from the same `window_at`
when passing both together. The window anchors acceptance/age filtering, while
membership and counts remain live: recovered recipients disappear, even if
their row supplied the cursor. Replay can restore older work behind a cursor;
restart discovery to include it. Listing oldest first does not promise delivery
FIFO. A whole-event lease appears as `receipt_processing` because it does not
identify which recipient is currently being routed.

The response declares `coverage=captured_and_backfill_recipients` and reports
unresolved older receipts without snapshots as `unattributed_receipts`. That
count is account-wide and uses only the acceptance/age window, even with other
filters. The API requires a read key, returns metadata with no-store caching,
and bounds reads to five seconds. Narrow filters and retry on
`event_backlog_read_timeout`. Apply the [backlog migration](adr/617-event-consumer-backlog-inspection.md)
and consumer origin migration before upgrading the API. Workflow rows represent
routing admission; after admission, inspect the workflow run and its steps for
execution status and recovery. `workflow_routing` identifies a pending workflow
admission that is not currently in a capacity wait, retry delay, or active lease.

Published and inbox envelopes use CloudEvents `datacontenttype` and the
`accountid` extension. The API accepts the older `data_content_type` and
`account_id` request spellings for existing clients.
Sources must be URI references, such as `urn:example:billing`,
`https://example.com/events`, or `billing.service`; percent-encode spaces.
CloudEvents webhook delivery also uses `accountid`. Receivers of the opt-in
CloudEvents format must read that extension instead of the previous invalid
`account_id` extension; the legacy Gregale JSON format keeps its existing shape.


An API key with `events:publish` can publish and send to an app inbox;
`queues:send` permits queue sends, and `queues:consume` permits
[pull leases](queue-pull-leases.md). Existing `deploy:write` keys continue to
work on these routes.

For a versioned event contract, register an immutable Draft 2020-12 JSON
Schema with `POST /v1/event-schemas` using a deploy key:

```json
{
  "source": "billing.stripe",
  "type": "invoice.paid",
  "version": "v1",
  "schema": {
    "type": "object",
    "required": ["amount"],
    "properties": {"amount": {"type": "number"}}
  }
}
```

Once the first version exists, producers must include
`"schemaversion":"v1"` in each matching publish. Gregale validates the
event data before accepting it; an unknown version or invalid payload
returns `422`. Schema versions cannot be changed in place. Use
`GET /v1/event-schemas?source=billing.stripe&type=invoice.paid` to inspect
registered versions. Sources beginning with `gregale.` are reserved for
platform events. Schemas cannot fetch external references.

Before publishing, check which enabled subscriptions would receive a sample.
Preview uses the router's matcher but does not persist the event or enqueue
invocations:

```bash
gregale event preview billing.stripe invoice.paid \
  --data '{"amount":150}'
```

The summary separates subscriptions that would receive the event from those
rejected by content filters. `--id` and `--time` can be supplied when a filter
inspects those CloudEvents attributes. `--json` returns the bounded samples and
complete match counts; previews require only the read API-key scope.

To confirm what Gregale reconciled for an app, list its active manifest
subscriptions directly:

```bash
gregale event subscription list APP
gregale event subscription list APP --json
gregale event deliveries APP
gregale event deliveries APP --state failed --json
```

This is useful after a deploy or manifest change: it shows the normalized
source, type, filter, and enabled state that the router will use.

To discover older retained events matching one current ordinary subscription,
use the read-only historical replay preview:

```bash
gregale event replay-preview APP --subscription-id SUBSCRIPTION_UUID \
  --from 2026-10-01T00:00:00Z --until 2026-10-06T00:00:00Z --limit 50
```

The API is `GET /v1/apps/{slug}/event-subscriptions/{subscriptionID}/replay-preview`;
the Go client is `pkg/api.Client.PreviewEventReplay`. The range uses **platform
acceptance time** `[from, until)`, independent of producer event time. Events
accepted before the subscription was created can match its current filter. The
preview returns event metadata and receipt links without creating deliveries.
Work-bound subscriptions, workflow starts and object notification declarations
are outside this subscription-specific preview surface. Workflow starts have a
separate read-only preview described below.

Each page examines at most `--limit` retained envelopes (default 50, maximum
100), including nonmatches. Counts apply to that page. An empty matching page
may still have `next_after`; continue with `--after` and the same app,
subscription and range. The cursor preserves the first page's `cutoff_at` and
subscription revision. A changed or disabled subscription requires restarting
the preview. `original_recipient` distinguishes `captured`, `not_captured` and
legacy `unknown` membership; it does not imply successful delivery or define
which events a future backfill runner would execute.

Settled receipts are kept for the plan's [settled-receipt window](#settled-receipt-retention) after **routing settlement**; unresolved
receipts can survive longer. `earliest_retained_at` is account-wide and does not
prove gap-free history. `history_complete` is always false. Retention can remove
rows between pages, and delayed commits of older acceptances can change visible
membership. This preview does not pin events or provide a frozen export. See
[ADR-645](adr/645-subscription-retained-event-replay-preview.md) for the contract.

To inspect historical workflow starts captured when each event was accepted,
use the separate workflow replay preview:

```bash
gregale event workflow-replay-preview APP --workflow-name paid \
  --from 2026-10-01T00:00:00Z --until 2026-10-06T00:00:00Z --limit 50
```

The API is `GET /v1/apps/{slug}/workflow-event-replay-preview`; the Go client is
`pkg/api.Client.PreviewWorkflowEventReplay`. It scans retained envelopes in the
same half-open platform acceptance-time range and reports whether the named
workflow was captured, whether its captured trigger filter matches, the latest
routing checkpoint, and whether the durable `(outbox, workflow recipient)`
admission receipt exists. A retained run ID and status are included when the
run still exists. The durable admission receipt remains visible after run
retention expires, so a pruned run is still recognized as deduplicated.

`potential_admission_count` counts matching captured recipients without an
admission receipt. It estimates future admission impact; it does not check
current account quota, app availability, or a future replay policy. The preview
does not start workflow runs or create deliveries. It does not evaluate the
current workflow definition for events where the workflow was not captured;
those are reported as `not_captured`, while legacy envelopes without recipient
snapshots are `unknown`. Counts are page-local, `history_complete` remains
false, and continuation is not a frozen export. See
[ADR-793](adr/793-workflow-event-replay-preview.md) for the contract.

To start a currently eligible app workflow for retained events whose immutable
recipient snapshot excluded it, create a durable workflow backfill:

```bash
gregale event workflow-backfill APP --workflow-name paid \
  --from 2026-10-01T00:00:00Z --until 2026-10-06T00:00:00Z --yes
gregale event backfill get JOB_UUID
gregale event backfill items JOB_UUID --state enqueued
```

The job snapshots the current workflow definition and trigger. It considers
only events with known recipient membership, a settled delivered receipt, no
captured membership for this app workflow, and a matching trigger. Unknown
legacy snapshots, captured workflows, unsettled receipts and prior admission
receipts are skipped. The durable `(event, app workflow)` admission receipt
deduplicates future jobs even after run retention removes the linked run.
`enqueued` means the run was admitted; monitor the run separately for
completion. Retryable quota and temporary target failures can be retried with
`gregale event backfill retry JOB_UUID --yes`. The range bound, active-job
quotas, retained-history coverage and pruning protection follow subscription
backfills. See [ADR-794](adr/794-durable-workflow-event-backfill.md).

To create actual independent deliveries for eligible historical events, start
a durable backfill for the same ordinary subscription:

```bash
gregale event backfill create APP --subscription-id SUBSCRIPTION_UUID \
  --from 2026-10-01T00:00:00Z --until 2026-10-06T00:00:00Z --yes
gregale event backfill get JOB_UUID
```

Backfill uses platform acceptance time and the half-open range `[from, until)`;
the server records a fixed cutoff when the job is created. The requested range
can span at most the plan's [settled-receipt window](#settled-receipt-retention). The target must be a current enabled ordinary
subscription; work-bound subscriptions are unsupported. The job snapshots its
target declaration, so later subscription changes do not change that job.

The duplicate policy is `skip_existing`. If the original acceptance snapshot
already captured this consumer, membership is unknown, a target recipient row
already exists, or the original receipt is not settled, that envelope is
skipped. Only matching, settled receipts with a known snapshot and a definitely
absent target can create a new recipient. Gregale leaves the original
`recipient_snapshot` unchanged. Backfill uses the normal independent recipient
lease, capacity, retry and handler lifecycle; it does not establish FIFO
ordering. `enqueued` means the invocation was admitted, not that the handler
completed.

The job scans at most 100 envelopes per page and holds at most 100 pending or
processing target deliveries at once. Up to three jobs can run per account,
with one active job per subscription. A running job protects the part of its
requested range that its scan has not reached yet from normal settled-receipt
pruning. Retryable failed deliveries keep
their source receipts through the job’s 30-day recovery window; other payloads
follow ordinary retention while the job’s per-envelope outcome counts remain
available for 30 days after completion. `earliest_retained_at` is a coverage
warning, not proof of a complete archive; already expired envelopes cannot be
recovered. Poll status until it leaves `running`; the status includes a
separate `retryable_failed` count. A job ends as:

- `completed` or `completed_with_failures` once every envelope settles;
- `cancelled` after `gregale event backfill cancel JOB_UUID --yes`
  (`POST /v1/event-replays/{jobID}/cancel`);
- `expired` when it makes no progress for 24 hours, for example while its
  target subscription stays paused or circuit-open;
- `failed` when processing the job fails ten times in a row; `last_failure`
  names the class of the last failure (`conflict`, `invalid_job`, `timeout`
  or `internal`). Between failures the job waits from 5 seconds up to an
  hour, and other jobs keep running.

When a job ends early, deliveries already admitted finish and the rest are
dropped: their items become `cancelled`, and their receipts go back to normal
retention. An ended job cannot be retried; create a new one. Retry a bounded batch of
eligible routing failures with
`gregale event backfill retry JOB_UUID --limit 100 --yes`; handler failures,
retries and dead letters continue through their existing lifecycle. Completed
job metadata and per-envelope outcomes are retained for 30 days. See
[ADR-639](adr/639-durable-subscription-event-backfill.md) for the full contract.

Inspect outcomes when you need to locate a failed or skipped event:

```bash
gregale event backfill items JOB_UUID --state failed --limit 50
```

The command prints a continuation command when another page is available. Keep
the job and state filter unchanged when following its cursor. Each item shows
the event identity, acceptance time, routing state, attempts and bounded
failure details; it never includes event data and remains readable after the
source envelope expires. While the original receipt is retained, `receipt_url`
opens unified delivery inspection; `attempt_history_url` opens the consumer's
handler attempts when its delivery provenance and current app ownership are
available. Links are omitted after their source expires, including if a new
event reuses that source and ID. Item states are live while a job is running, so for a
complete filtered view, inspect after the job reaches a terminal state.

Inspect one published event across captured and backfilled consumers:

```bash
gregale event get --source billing.stripe --id evt-123
gregale event get --source billing.stripe --id evt-123 --json
```

Publish returns a `receipt_url` and `Location` header for
`GET /v1/events/receipt?source=SOURCE&id=ID`. Identical publish retries keep the
original `accepted_at`. The receipt includes every source/type candidate captured
at acceptance, even before an invocation exists, and separates routing from
handler execution. Whole-snapshot counts cover pending, processing, filtered,
enqueued, and failed recipients. `recipient_count` and `routing_summary` retain
their acceptance-snapshot meaning; `backfill_recipient_count` and
`backfill_routing_summary` count added consumers separately. Recipients label
their `origin` and link to the originating backfill job while it is retained.
Handler outcomes preserve cancellation,
supersession, expiry, and dead letters. A `cancel_pending` operation reports its
cancellation receipt rather than a handler invocation.

Use `--limit` (1–200, default 100) and `--after` with `next_after` for larger
fanouts. Pagination follows captured recipient order, then stable appended
backfill positions even during retries, replay, or job pruning; outcomes can
change between pages. Inspection order does not guarantee delivery order.
`routing_settled_at` means routing
has settled, including failures, and `retain_until` is the plan's settled-receipt window later. These
fields are absent while routing is active. Legacy receipts without snapshots
report `snapshot_captured=false`; their membership cannot be reconstructed.
Whole-event routing exposes retained checkpoints, so a pending recipient can
still have an active parent worker.

Each recipient includes its routing attempts, retry time, error, replay count,
and routing history URL, plus the retained original invocation or cancellation.
`record_unavailable` means the original execution record was not found after
routing; it does not assert success. Execution records have independent
retention. The JSON response supplies applicable selective recovery requests;
calling them requires the existing write scopes and rechecks current eligibility.
A retryable backfill routing failure has the same selective routing recovery
action, even while sibling events in the job are pending. Recovery resets just
that delivery and keeps the job item and receipt consistent. Backfill handler
failures use the same handler replay and dead-letter recovery as captured
consumers. See [ADR-646](adr/646-unified-backfill-delivery-inspection.md).
Routing replay and in-place dead-letter replay remain visible on the original
receipt. Generic handler replay creates a new invocation with ledger-owned parent
and root identity. The receipt preserves the original failure and adds `recovery`
with `latest_replay`, `retained_replay_count`, and `history_url`. A completed latest
replay means that replay succeeded; text inspection labels it `recovered`.
Recovery requests target the latest retained replay, and are absent while that
replay is active or completed. Independent consumer outcomes remain separate.

Recover one captured application or workflow consumer, or a backfilled
application consumer, directly from its receipt:

```bash
gregale event recover --source billing.stripe --id evt-123 \
  --subscription SUBSCRIPTION_ID --dry-run
gregale event recover --source billing.stripe --id evt-123 \
  --subscription SUBSCRIPTION_ID
```

The command follows recipient pages automatically and selects the receipt's
current `routing_replay`, `handler_replay`, `keyed_handler_replay`, or
`dead_letter_replay` action. It sends one selective request through the existing
replay API. Successful consumers retain their original deliveries. Routing
recovery uses the accepted subscription snapshot; handler recovery targets the
latest eligible execution in its trusted replay lineage.
For workflow recipients, `routing_replay` recovers admission of the captured
workflow definition. Once admitted, inspect the linked workflow run for step
status and execution recovery. Text inspection shows the workflow name and
retained run ID/status alongside its routing outcome.

`--dry-run` reads the receipt without changing delivery state. It reports the
selected action or explains why recovery is unavailable, including successful
consumers, active deliveries/replays, cancellation, unavailable execution
records, and workflows whose admission already completed. A dry run exits zero
when it can inspect the selected recipient, including when no action is available.
Actual recovery exits nonzero if the receipt offers no action. Missing recipients, legacy
receipts without captured membership, unsupported actions, and API failures
also exit nonzero. `--json` emits event/subscription identity, `dry_run`,
`status` (`available`, `queued`, or `unavailable`), the selected `action`, and
the replay `result` or an unavailable `reason`.

Availability can change after inspection. The replay endpoint rechecks current
ownership, scopes, deadlines, and claim eligibility; a rejected request is
reported without trying another recovery path. `queued` means recovery was
accepted, not that the handler completed. Use `events inspect` and `events
attempts` to follow it. At-least-once delivery and application side-effect
deduplication still apply.

Plain handler replay uses `POST /v1/invocations/{id}/replay` (or
`gregale invocation get --replay INVOCATION_ID`). Each failed parent creates
one durable child. Repeated and concurrent requests return that child even
with different request keys or after success; further recovery targets the
failed child. Customer self-service and account-operator replay share this
identity and preserve the captured customer and environment. If the child has
been pruned, its retained parent returns `invocation_replay_unavailable` and
receipts suppress its handler replay action. Existing accepted replay IDs
remain readable when an old deployment pin expires. Delivery remains at least
once; applications still deduplicate external side effects. See
[ADR-612](adr/612-durable-plain-invocation-replay.md).

Failed keyed handlers use `keyed_handler_replay`, which calls
`POST /v1/invocations/{id}/replay-keyed`. It preserves the captured policy
revision, key, fairness controls and environment, and joins the end of that
key's queue. It does not supersede newer pending work or restart debounce.
The original pending expiry remains effective: expired work needs a new
publication with a new event ID and fresh lifetime. Retrying the same accepted
event does not renew its deadline. Generic invocation replay rejects keyed
work so it cannot bypass its claim gate.

```bash
gregale invocation get --replay-keyed INVOCATION_ID
```

Repeated keyed recovery requests return the same child, even after it completes.
To recover again, target that child after it fails. If the child has been pruned
while the parent remains, the parent cannot create another execution; receipts
suppress that action. Queue-bound dead letters retain their existing in-place
replay path. See [ADR-609](adr/609-safe-keyed-invocation-replay.md) for ordering,
expiry and retention behavior.

Keyed dead-letter replay keeps the original receipt and sequence. It waits
for any same-key invocation or broker delivery already running, including a
later sequence. An expired lease must be recovered before the replay can
proceed. Once that ownership is resolved, pending work follows sequence order;
other keys remain eligible. Replay preserves the original pending expiry.
See [ADR-610](adr/610-keyed-dead-letter-replay-claim-exclusion.md).

Inspect the original handler's delivery attempts and its trusted replay children:

```bash
gregale event attempts --source billing.stripe --id evt-123 \
  --subscription SUBSCRIPTION_ID --limit 100
```

This reads `GET /v1/events/receipt/attempts`; receipt recipients expose its
`attempt_history_url`. Each entry contains invocation ID, replay generation,
attempt number, start and finish times, outcome, error and next retry time.
An expired dispatch lease settles as `unknown`: the history does not prove
whether the handler ran or applied side effects. Use application idempotency.
Pagination uses the returned `next_after` with `--after`, newest attempt first.

Only recorded, retained attempts are shown. History starts with claims made
after the attempt-ledger upgrade and is not backfilled. Closed attempts expire
after at most 30 days, earlier for shorter result retention or invocation
deletion; running attempts are not pruned. An empty history does not establish
that no delivery occurred. See [ADR-611](adr/611-invocation-backed-event-attempt-history.md).

List a consumer's retained replay executions, newest first:

```bash
gregale event get --source billing.stripe --id evt-123 --subscription SUB
gregale event get --source billing.stripe --id evt-123 --subscription SUB --json
```

This reads `GET /v1/events/receipt/replays` with the same source, ID and captured
`subscription_id`. Use `--limit` and the returned `next_after` with `--after` to
read older pages. Replay cursors are separate from recipient cursors and remain
usable when their execution anchor expires. Root identity and its creation time
survive on descendants, so expired intermediate rows cannot sever recovery or
attach it to a later reuse of the event identity. Counts cover retained rows,
not lifetime replays. When all replay records expire, absence of `recovery` does
not establish that recovery never happened. Older generic replays without trusted
lineage remain in app delivery history; guest headers cannot reconstruct it.

Captured target IDs remain, but current metadata and recovery actions
are unavailable when the target no longer belongs to the account.

After publishing, use `events deliveries` to see the matching event id,
delivery state, attempt count, and last lifecycle timestamp without searching
the account-wide invocation ledger. The history includes operator replays and
labels each row's invocation source so original deliveries and replays are
easy to distinguish. `--event-id` narrows the view by event ID;
add `--event-source` to select one exact event identity when IDs are reused by
different sources. `--state` can focus on pending, failed, or dead-lettered
deliveries.

The same command also reports terminal recipient routing failures that
occurred before Gregale created an invocation. These rows include the event,
subscription, routing attempt count, stable `failure_code`, `retryable` signal,
failure time, and error. `invalid_subscription` and `target_unavailable` point
to configuration or target state that should be fixed before replay;
`target_lookup_failed` and `invocation_enqueue_failed` identify transient errors
that may succeed on replay. Older failures recorded before classification was
available use `unknown`. Use `--fanout-before` with `next_fanout_before` from
`--json` to page through that failure history independently of invocation
deliveries.

The latest routing checkpoint is useful for finding current failures, but an
operator replay changes that checkpoint. To inspect the immutable sequence of
routing outcomes and replay requests for one published event, use
`events fanout-history`:

```bash
gregale event fanout history APP \
  --event-source billing.stripe \
  --event-id evt-123
```

History is ordered newest first. Each recipient retains at most 128 detail rows
and 64 KiB of logical detail bytes; unprotected detail expires after thirty days,
including while the event remains pending. Compaction prioritizes the latest
outcome, real failure and replay request. Error details are bounded to 1,024 UTF-8
bytes and clipped rows set `details_truncated`.

Repeated consecutive capacity waits with the same scope update durable counters
instead of appending detail. JSON output includes `coverage=bounded_recorded_outcomes`
and recipient `summaries`: cumulative deferrals, wait first/last times, coalesced
and compacted counts, retained records/bytes, and the boundary of removed detail.
Summaries describe recorded observations and survive with the receipt; older
unrecorded transitions cannot be reconstructed. The last capacity scope is the
most recent recorded wait, including after recovery.

Use `--subscription-id` to narrow the view and `--before` with `next_before` from
JSON output to page through older rows. Cursors remain valid when their detail
row is removed, although an older page may become empty. Summaries reflect current
observations independently of the page cursor. Recovery uses the durable recipient
checkpoint and does not depend on retaining every history row. See
[ADR-616](adr/616-bounded-event-routing-history.md).

To retry one terminal pre-invocation failure, pass its event ID, source, and
subscription ID from the failure row:

```bash
gregale event replay APP \
  --event-id evt-123 \
  --event-source billing.stripe \
  --subscription-id 5ef2a270-2c12-4ddd-a2a7-a0873995f7c8
```

For receipts using independent recipient routing, replay is available as soon
as that recipient fails, even while siblings are active. Legacy receipts must
wait until the event's fanout receipt settles. Replay queues only that recipient
and keeps the event payload and recipient configuration
captured when the event was accepted. A replay therefore uses the same filter
and target app; fix persistent routing or app problems before retrying. Other
recipients that already succeeded or failed are not rerun.

After a transient outage, replay retryable failures for an app in bounded
batches:

```bash
gregale event replay-retryable APP --limit 100 --yes
```

This command requeues only terminal failures classified as retryable, oldest
first, and never more than 100 recipients per call. Add both `--event-source`
and `--event-id` to scope it to one published event. `--yes` confirms the
batch; repeat the command when the response reports `has_more: true`. A queued
event can accept additional replay batches while pending. Independent recipient
routing also permits replay while siblings are processing. A legacy event with
an active whole-event worker is left alone and continues to report more
failures; repeat after that event settles.
Configuration failures such as an invalid
subscription or unavailable target remain untouched for explicit repair and
single-recipient replay.

The machine-readable event contract is published in
[`api/asyncapi.yaml`](../api/asyncapi.yaml), including the authenticated
`POST /v1/events:publish` ingress.

### Diagnosing ordered routing waits

`gregale event backlog --waiting-reason ordering_blocked` lists recipients waiting for earlier routing in their captured app/policy/key lane. Filtering happens before recipient pagination and consumer aggregation. Consumer summaries include `ordering_waiting_recipients`.

Each blocked recipient includes `ordering_blocker`: the earliest unresolved event and subscription, acceptance time and age, current routing state, next retry time when pending, and a receipt URL. Event data and resolved work keys are not exposed. The lookup uses the same predicate as recipient claims and is recomputed on each read, so recovering a blocker clears the wait without a projection update.

Active routing and shared receipt leases take precedence over subscription controls and ordering; live subscription controls precede ordering, which precedes recorded capacity and retry backoff. These diagnostics describe event routing, not an invocation already admitted to its execution lane. Whole-receipt routing excludes earlier positions in its own snapshot because it walks those positions serially.

### Bulk recovery of failed application consumers

Use a recovery preview to select retained terminal **routing** failures captured
at publication. Filters are exact subscription ID, event source, event type,
failure classification, and minimum time since the recorded failure. Retryable
failures are selected by default; `--include-non-retryable` explicitly broadens
the selection.

```sh
gregale event recovery-preview invoice-worker \
  --event-type invoice.created --failure-code invocation_enqueue_failed --min-age 10m

gregale event recovery create invoice-worker \
  --event-type invoice.created --failure-code invocation_enqueue_failed --min-age 10m \
  --rate 10 --yes

gregale event recovery get JOB_ID
gregale event recovery items JOB_ID --limit 100
gregale event recovery cancel JOB_ID --yes
```

The preview is read-only and returns at most 100 sample recipients. Its match
count is exact up to 10,000; `exceeds_job_limit` means the reported count is a
lower bound and the filters must be narrowed. Creation captures its own current
selection and rejects more than 10,000 recipients without creating a partial
job. New failures after creation are excluded. An empty selection completes
immediately.

Jobs are durable and account scoped. At most three jobs may run per account.
Each job processes at most 1–100 recipients per one-second window (default 10),
including skipped items; the persisted budget survives restarts and concurrent
workers. Processing may be slower under scheduler load. Jobs expire after 24
hours, cancelling remaining items. Completed/cancelled jobs and their
metadata-only item outcomes are retained for 30 days.

Each retry checks the selected recipient's failure identity before atomically
reopening it and recording `queued`. If another recovery already changed it,
the item becomes `skipped` with reason `changed`; missing retained receipts and
deleted targets are also skipped. Legacy whole-receipt routing claims defer
recovery until they settle. No new event identity is published, immutable
recipient snapshots are preserved, and successful sibling consumers keep their
outcomes. Existing work ordering and deduplication rules apply. Replaying an
older terminal failure cannot undo younger deliveries that already completed.

Cancellation and processing share a job lock. Cancellation waits for any retry
transaction already in progress, then cancels remaining selected items. Already
queued retries continue through normal delivery. `completed` means every
selection item was queued or skipped; it does not mean invocation execution
succeeded. Inspect receipts and existing attempt history for delivery outcomes.

API endpoints:

- `POST /v1/apps/{slug}/event-recoveries/preview`
- `POST /v1/apps/{slug}/event-recoveries` (supports `Idempotency-Key`)
- `GET /v1/event-recoveries/{jobID}`
- `GET /v1/event-recoveries/{jobID}/items?after=POSITION&limit=100`
- `POST /v1/event-recoveries/{jobID}/cancel`

Preview and reads require `apps:read` or `admin`; creation and cancellation
require `deploy:write` or `admin`, with the existing MFA policy. Responses use
`coverage: captured_application_recipients`. Historical backfill recovery,
workflow recovery, and failures after invocation admission retain their existing
recovery APIs; they are excluded from this job selection. Envelope data, work
keys, and raw errors are not returned or copied into recovery item metadata.
Receipt retention is independent: an expired receipt is skipped rather than
reconstructed from the job.

### Pause and drain a subscription

Pause a captured application consumer during maintenance or an outage:

```sh
gregale event subscription pause invoice-worker SUBSCRIPTION_ID --yes
gregale event subscription get invoice-worker SUBSCRIPTION_ID
gregale event backlog --app invoice-worker --waiting-reason subscription_paused

gregale event subscription resume invoice-worker SUBSCRIPTION_ID --rate 10 --yes
gregale event backlog --app invoice-worker --waiting-reason subscription_rate_limited
```

Pausing stops new routing admissions for that subscription ID. It does not
change subscription matching: enabled declarations continue capturing events.
Other consumers continue according to their existing capacity and ordering
rules. Invocations already admitted, including their execution retries,
continue. Pause and admission serialize on the same consumer lock; a successful
pause response follows any admission transaction already in progress.

Pause and drain rate are live operator controls stored independently from
manifest declarations. They survive restarts, redeployment and subscription
removal. A removed subscription with retained controls can still be inspected
and resumed using its captured ID. A newly created subscription ID has its own
controls. Deleting a target app lets its waiting recipients settle as
unavailable targets rather than holding them indefinitely.

Resume defaults to ten new routing admissions per one-second window. Rates
1–100 are supported; `--rate 0` removes pacing. This rate also applies to future
publications after the backlog drains. The persisted budget is shared by all
workers and is reserved in the admission transaction, so rollbacks restore it.
Repeating a resume with the same active rate does not reset the budget.
Normal claim polling and previously scheduled retry times may delay draining.
Pause and rate waits do not consume the recipient's routing retry budget.

Status reports pending and processing recipient counts, the oldest pending
acceptance time and age, pause state, and rate. Counts cover retained publication
and historical backfill recipients for that application subscription. Backlog
filters run before pagination and consumer aggregation. Active routing and
shared receipt leases take precedence in waiting reasons; live subscription
controls precede ordering, recorded capacity and retry backoff.

Pending captured receipts are retained until routing settles; pausing does not
start an expiry timer. Paused events consume the existing account retained-event
and byte budgets. Once those budgets are full, publication is rejected by the
normal storage admission rules. Resume retains original event identities and
snapshots, and uses the existing ordering and deduplication behavior. Settled
receipt retention and invocation retention follow their existing rules.

API controls are available at:

- `GET /v1/apps/{slug}/event-subscriptions/{subscriptionID}/delivery-control`
- `POST /v1/apps/{slug}/event-subscriptions/{subscriptionID}/delivery-control/pause`
- `POST /v1/apps/{slug}/event-subscriptions/{subscriptionID}/delivery-control/resume`
  with `{ "rate_per_second": 10 }` (or `{}` for the default).

Reads require `apps:read` or `admin`; writes require `deploy:write` or `admin`,
with the existing MFA policy. Controls apply to captured application routing
admission, including application backfill recipients. Workflow and object
notification recipients have their own delivery paths. Pre-snapshot legacy
receipts use their original dynamic routing path.

### Consumer routing health and maintenance alerts

Inspect one application's subscription:

```sh
gregale event subscription health my-app <subscription-id> --window 15m
```

The health endpoint at
`GET /v1/apps/{slug}/event-subscriptions/{subscriptionID}/health?window=15m`
reports live pending and processing counts, oldest pending age, pause duration,
scheduled retries per second, terminal failure percentage, successful routing
rate, and acceptance-to-routing p95 latency. Routing ends at invocation admission;
execution completion is observed separately. Historical backfill latency includes
the event's original age. Supported windows are `5m`, `15m`, `1h`, `6h`, and `24h`.

Rates count retained `fanout_attempt` and `backfill_attempt` outcomes. Terminal
failure percentage uses failed / (failed + enqueued); recovery can record more
than one outcome per recipient. Retry rate counts error outcomes returning to
pending and excludes capacity, pause, and pacing waits. Coverage is
`bounded_recorded_outcomes`. `history_compacted=true` means windowed rates are
incomplete retained samples. These are consumer API observations; they do not
create subscription-labelled Prometheus series.

Create an alert with `gregale alert create --event-subscription-id <uuid>` and one
of these metrics:

| Metric | Unit |
| --- | --- |
| `event_pending_recipients` | Pending routing recipients |
| `event_oldest_pending_seconds` | Age of oldest pending recipient |
| `event_retry_rate_per_second` | Recorded retries scheduled / second |
| `event_terminal_failure_pct` | Recorded terminal failure percentage |
| `event_routing_latency_p95_seconds` | Acceptance-to-routing p95 seconds |
| `event_paused_seconds` | Continuous duration of current pause |
| `event_drain_rate_per_second` | Recorded successful routes / second |

For example, use `--metric event_oldest_pending_seconds --comparison gt
--threshold 600 --window-spec 15m` with the usual app, name, webhook URL, and
secret flags. A failure alert can use `event_terminal_failure_pct`, `gt`, and
`5`. Consumer rules require webhook actions, windows up to 24h, and an immutable
subscription selector. Failure percentages need 20 terminal observations; latency
needs one success. Drain-rate alerts require a pending or processing backlog.
History-dependent alerts skip compacted windows.

Pausing suppresses all consumer health alerts except `event_paused_seconds`.
A separate pause alert detects maintenance that runs too long. Suppressed or
insufficient observations set the rule to `unknown`; existing queued webhook
notifications continue through the alert delivery outbox. Repeated pause requests
preserve pause duration; resume resets it.

### Per-consumer routing retry policies

Application subscriptions can configure routing retries in YAML:

```yaml
event_triggers:
  - source: orders
    type: order.created
    retry:
      max_attempts: 20
      initial_backoff: 2s
      max_backoff: 2m
      max_retry_duration: 30m
      jitter: true
```

The equivalent TOML declaration uses `[triggers.event.retry]` after its
`[[triggers.event]]` entry. A retry block defaults to 12 attempts, 5s initial
backoff, a 5m cap, unlimited duration budget, and jitter enabled. Backoffs use
whole milliseconds up to 1h; attempts range from 1 to 100 and duration budgets
from 0 to 7d. Maximum backoff must be at least the initial delay.

Inspect or replace settings for future events:

```sh
gregale event subscription-retry get my-app <subscription-id>
gregale event subscription-retry set my-app <subscription-id> --max-attempts 20 --initial-backoff 2s --max-backoff 2m --max-retry-duration 30m --yes
gregale event subscription-retry reset my-app <subscription-id> --yes
```

GET, PUT, and DELETE at
`/v1/apps/{slug}/event-subscriptions/{subscriptionID}/retry-policy` inspect,
replace, or reset current subscription configuration. API replacement uses
`max_attempts`, `initial_backoff_ms`, `max_backoff_ms`,
`max_retry_duration_ms` (default 0), and `jitter` (default false). Reads require
`apps:read` or `admin`; writes require `deploy:write` or `admin` and MFA.

The duration budget counts failed routing attempt time and reserves scheduled
retry delays. A retry is scheduled only if its entire delay fits. Pause, pacing,
capacity, ordering, and worker downtime add no budget cost. This budget does not
impose an event age deadline or interrupt an in-flight admission. Delay grows
exponentially up to the configured cap; jitter spreads it between 1ms and that
cap using a stable per-recipient, generation, and attempt hash.

Policies are captured at event publication or backfill job creation. Changing
or resetting configuration leaves those snapshots intact. Reapplying a manifest
without a retry block restores legacy defaults for future events: 12 attempts,
5s initial backoff, 5m maximum, no duration bound, and jitter disabled. Legacy
snapshots use those same defaults. Apply the migration and upgrade all routing
workers before enabling custom policies.

Receipt routing diagnostics expose the captured `routing_retry_policy`,
`retry_spent_ms`, and `retry_stop_reason`. Retained routing attempt history
also records stop reason: `non_retryable`, `max_attempts`, or `max_duration`.
The underlying `failure_code` remains visible. Operator replay starts a fresh
retry generation under the same snapshot, preserving ordering and deduplication.
Invocation execution retries have their separate existing policy.

### Automatic consumer circuit breakers

Enable an opt-in circuit breaker for an application event subscription:

```sh
gregale event subscription-circuit set my-app <subscription-id> --yes
gregale event subscription-circuit get my-app <subscription-id>
gregale event subscription-circuit reset my-app <subscription-id> --yes
gregale event subscription-circuit disable my-app <subscription-id> --yes
```

Defaults open a circuit at 50% routing failures with at least 20 outcomes in
300 seconds. Configure `--failure-threshold-pct`, `--min-samples`,
`--window-seconds`, `--cooldown-seconds`, `--probe-successes`,
`--recovery-max-rate`, and `--recovery-seconds`. Configuration is operational;
deployments do not declare or overwrite it.

An open consumer waits for the cooldown (default 60s). Available pending work
then probes one delivery at a time. Three successful admissions start paced
recovery: one routing permit per second, doubling every 10s to the configured
cap (default 10/s). After the recovery interval (default 60s), it closes. A
probe or recovery routing failure reopens it. A lost probe lease also reopens
it when no durable outcome exists. Filtered events and capacity waits release
a probe without counting as success or failure. Healthy consumers continue.

Breaker waits spend no routing retry attempts or duration budget. It measures
routing failures before invocation admission; execution retries and failures
retain their existing policy. It does not replay dead letters automatically.
Existing in-flight routing may finish, and a permit consumed by a neutral
outcome can reduce actual throughput. Ordering and deduplication still apply.

Status, delivery controls, and consumer health expose durable state, transition
reason, cooldown, probe status, recovery rate, and incomplete history. Backlog
waiting reasons include `circuit_open`, `circuit_probe_wait`, and
`circuit_recovery_rate_limited`. State transitions occur with routing work;
reading status does not schedule probes. Threshold checks occur at most once
per second per consumer. Compacted observation windows prevent automatic
threshold decisions and recovery completion.

Manual pause always stays in effect until explicitly resumed. Reset closes an
enabled breaker with a fresh observation window; disable removes it. Both
preserve manual pause and pacing. Enabling or replacing policy also starts a
fresh observation window. Terminal failures remain recoverable using the
existing replay and recovery commands.

The REST resource is
`/v1/apps/{slug}/event-subscriptions/{subscriptionID}/circuit-breaker`: GET
reads, PUT enables/replaces with defaults for omitted fields, DELETE disables,
and POST to `/reset` closes an enabled breaker. Policy fields are
`failure_threshold_pct`, `min_samples`, `window_seconds`, `cooldown_seconds`,
`probe_successes`, `recovery_max_rate_per_second`, and `recovery_seconds`.
Reads require `apps:read` or `admin`; writes require `deploy:write` or `admin`
and MFA. Apply the migration and upgrade all routing workers before enabling.

### Per-consumer delivery age limits

Set a wall-clock limit for newly admitted event deliveries:

```yaml
event_triggers:
  - source: inventory
    type: stock.changed
    retry:
      max_delivery_age: 15m
```

The same field is supported under `[triggers.event.retry]` in TOML. It defaults
to zero (no expiry), supports whole milliseconds up to 720h, and is captured
with each event recipient. Configure future events through the existing retry
policy API using `max_delivery_age_ms`, or the CLI:

```sh
gregale event subscription-retry set inventory-api <subscription-id> --max-delivery-age 15m --yes
```

This command replaces the routing policy; include other retry flags to retain
custom settings. Resetting the policy disables expiry for future events.
Captured events keep their original policy. Strictly ordered subscriptions
reject positive delivery age limits.

Age starts when Gregale accepts the event, using the platform timestamp. Pauses,
capacity waits, circuit cooldowns, retry backoff, and worker downtime all count.
Once the limit is reached, pending routing becomes a terminal failure with
`failure_code` and `retry_stop_reason` set to `delivery_expired`. The scheduler
can select it for expiry even while normal delivery is paused or circuit-blocked.
Active claims remain fenced until they finish or their leases are recovered.
Expiry spends no extra retry attempt or duration budget, and other consumers
continue independently. Already admitted invocations keep their execution lifecycle.

Receipt diagnostics show `delivery_deadline_at` and `delivery_age_override`.
Consumer health reports `expired_deliveries` in its observation window; these
are included in terminal failures. Expiry does not count as a routing failure
for circuit breakers. Retained failed-delivery listings and history keep the
expiry reason for inspection.

Replay preview includes `delivery_expired` and `delivery_deadline_at` for each
match, plus `expired_count`, under the current target policy. A historical
backfill uses its captured policy against the original event acceptance time.
To deliberately deliver stale work, request an explicit override:

```sh
gregale event replay inventory-api --event-source inventory --event-id evt-123 --subscription-id <subscription-id> --allow-expired
gregale event backfill create inventory-api --subscription-id <subscription-id> --since 2026-10-01T00:00:00Z --until 2026-10-02T00:00:00Z --allow-expired --yes
```

The equivalent replay/backfill request field is `allow_expired: true`.
Individual replay returns `409 Conflict` without it when the original deadline
has passed. The override applies to that replay generation, or to the explicitly
created historical backfill job. It is persisted and audited, while ordering,
deduplication, manual pause, and circuit controls still apply. Bulk retry and
recovery never silently override age; recovery skips items that expire after
selection. Apply the migration and upgrade all routing workers before enabling.

## Select schema versions per application consumer

Use `schema_versions` to upgrade consumers independently of event producers:

```yaml
triggers:
  event:
    - source: billing.*
      type: invoice.paid
      schema_versions: [v1, v2]
```

An omitted or empty list accepts every version, including unversioned events.
A nonempty list accepts only exact, case-sensitive identifiers and excludes
unversioned events. Select up to 16 unique versions; each identifier is 1–64
characters, starts with a letter or digit, and contains letters, digits, dots,
underscores, or hyphens. Versions may be selected before registration.

```sh
gregale event subscription-version get <app> <subscription-id>
gregale event subscription-version set <app> <subscription-id> --versions v1,v2 --yes
gregale event subscription-version reset <app> <subscription-id> --yes
```

The API resource is
`/v1/apps/{slug}/event-subscriptions/{subscriptionID}/schema-versions`: GET
inspects the selection, PUT accepts `{ "schema_versions": ["v1", "v2"] }`,
and DELETE resets it. PUT requires an array; an empty array accepts all versions.

Selection is captured when an event is published, or when a retained backfill
job is created. Updating a subscription affects future captures. Manifest
omission resets the selection. Existing captured deliveries without a selection
accept every version. Workflow and object notification subscriptions use their
existing behavior.

Incompatible versions settle as `filtered` with
`filter_reason: schema_version_mismatch`, create no invocation, consume no
routing retry attempt, and do not count as circuit breaker failures. Filtering
precedes pause, backoff, ordering, circuit, and expiry gates. Routing and retained
replay previews report `schema_version_mismatch_count`; backfills skip incompatible
versions as filtered items. Retained replay cursors become invalid when the
subscription selection changes.

## Preview a schema rollout

Before switching a producer to a new version, check consumer version coverage
and validate event data with a read-only rollout preview:

```sh
gregale event schema check billing.stripe invoice.paid \
  --version v2 --schema @invoice-v2.schema.json \
  --samples @invoice-samples.json \
  --from 2026-10-01T00:00:00Z --until 2026-10-08T00:00:00Z \
  --retained-limit 100
```

`invoice-samples.json` is an array of event **data** values, such as
`[{"amount":150,"currency":"TRY"}]`. Omit `--schema` to check an already
registered version. Schema and samples accept inline JSON, `@file`, or `-`
for stdin; only one input may use stdin. `--json` returns the complete response.

The API is `POST /v1/event-schemas:preview-rollout`, available to read keys
(`apps:read` or `admin`) with the same MFA requirements as schema inspection:

```json
{
  "source": "billing.stripe",
  "type": "invoice.paid",
  "version": "v2",
  "schema": {
    "type": "object",
    "required": ["amount", "currency"],
    "properties": {
      "amount": {"type": "number"},
      "currency": {"type": "string"}
    }
  },
  "samples": [{"amount":150,"currency":"TRY"}]
}
```

The response lists enabled application consumers whose source/type patterns
match, with `accepts_version` and `content_filter_present`, plus accepting and
excluding counts. Version acceptance alone does not evaluate content filters
or guarantee delivery. At most 1,000 consumers are returned; when
`consumers_truncated` is true, counts cover only the returned consumers.
Workflow and object notification subscriptions are outside this coverage.

Supply both `from` and `until` to check retained payloads. These are inclusive
start and exclusive end platform acceptance times; future end times are capped
at `observed_at`. The preview scans the newest 1,000 **account** receipts in
that range, including unrelated event types, then checks up to 100 matching
payloads, or the lower `retained_limit`. It validates original event data
against the proposed schema regardless of the original version.

`retained.scanned_count` counts account candidates; `examined_count` counts
matching results. Valid, invalid, and unreadable counts are separate. Envelopes
larger than 64 KiB, malformed envelopes, or inconsistent identities are
unreadable rather than schema-invalid. The aggregate validation byte budget is
4 MiB. Limit exhaustion sets `retained.truncated`; `history_complete` is always
false because retained sampling cannot establish complete historical coverage.
An empty sample is not evidence of compatibility.

Caller samples are limited to 20 data values of 64 KiB each; schemas are limited
to 64 KiB and cannot fetch external references. The request body is limited to
2 MiB and the observation times out after 15 seconds. Validation diagnostics
return a bounded first failing field path and reason, without payload values.
The preview registers no schema, publishes no event, and changes no delivery.
Go, Node, and Python clients expose `previewEventSchemaRollout` (following each
SDK's naming conventions).

## Operating event delivery

Consumer execution health, bulk and paced recovery, recovery notifications,
producer keys, transactional consumer writes, subscription capacity commands
and deadline alerts are documented in
[Event operations](events-operations.md).
