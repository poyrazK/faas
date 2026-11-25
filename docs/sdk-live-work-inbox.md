# Watch a live work inbox

Use the preview watcher when a dashboard or agent already knows a business
record, such as one CSV import. It yields typed [work inbox](sdk-work-inbox.md)
snapshots with current pipeline and action states, review links, and stable IDs
for approved follow-up operations.

## Serve the watch from your application

1. Authenticate the viewer and verify current access to the business record.
   Choose an opaque `resource_id` and a private channel bound to the viewer,
   record, and app. Return the `resource_id` and `business_ref` as the watch
   target. Issue a short-lived subscription ticket for only that channel and
   recheck access on each ticket request.
2. Serve a private JSON snapshot route. Check access to the business record
   even when its inbox is empty. Use `readWorkInboxSnapshot(inbox, target,
   principal, options)` in Node or `read_work_inbox_snapshot(inbox, target,
   principal, timeout=...)` in Python. These helpers read existing authorized
   inbox pages. Use the authenticated principal and a server-derived target.
   Check record access again before sending a response and disable shared caches.
3. After committing a change, publish `workInboxInvalidation(resourceId,
   changeId)` or `work_inbox_invalidation(resource_id, change_id)` to that
   channel. Reuse the same change ID if delivery is retried. The four-field
   notification contains the resource ID and change ID. Keep receipts,
   review URLs, credentials, and business data in the authorized read path.

On revoked access, deny snapshot and ticket requests and revoke active channel
access where supported. In the callback wrappers, map the application's access
denial (including a deliberate 404 used to hide the record) to
`LiveWorkInboxError("unauthorized")`. A watch validates every snapshot against its
original target. Discard locally displayed entries when the watch ends with an
access denial.

## Consume snapshots

Node exports the watcher and its realtime adapter from `@gregale/sdk-node/preview`
and `@gregale/sdk-node/browser`. The server-side snapshot helper is available
from `/preview`.

```ts
import {
  createBrowserRealtimeTicketSocketFactory,
  realtimeLiveWorkInboxSource,
  watchWorkInbox,
} from "@gregale/sdk-node/preview";

for await (const snapshot of watchWorkInbox({
  target,
  source: realtimeLiveWorkInboxSource({
    url,
    channel,
    webSocketFactory: createBrowserRealtimeTicketSocketFactory(fetchFreshTicket),
  }),
  loadSnapshot: ({ signal, timeoutMs }) => loadAuthorizedInbox({ signal, timeoutMs }),
  timeoutMs: 120_000,
  signal: controller.signal,
})) {
  render(snapshot.items, { truncated: snapshot.truncated });
}
```

`fetchFreshTicket` calls the application's ticket route and checks that the
returned channel, endpoint, and target match the original watch. Use bounded
HTTP requests in both callbacks. Abort the controller or break the loop to
close the watch.

```python
from contextlib import aclosing

from faas_sdk.preview import realtime_live_work_inbox_source, watch_work_inbox

async with aclosing(watch_work_inbox(
    target=target,
    source=realtime_live_work_inbox_source(
        connect=connect_with_fresh_ticket, channel=channel,
    ),
    load_snapshot=load_authorized_inbox,
    timeout=120,
)) as snapshots:
    async for snapshot in snapshots:
        render(snapshot["items"], truncated=snapshot["truncated"])
```

`connect_with_fresh_ticket` fetches a ticket for the original target and opens a
session using `RealtimeWebSocketClient.connect()`. `load_authorized_inbox`
receives the remaining deadline in seconds. Cancel the consuming task to
interrupt a pending pull and use `aclosing` to close on an early break.

## Delivery behavior

The iterator subscribes lazily. After subscription or reconnect, it reads the
current snapshot even if every notification was missed. It coalesces refresh
signals and suppresses equal snapshots within a connection. While connected it
also refreshes periodically, every 15 seconds by default. Each snapshot replaces
the previous snapshot. The deadline and snapshot read budget bound the lifetime
and work of each iterator.

The snapshot helpers read at most 10 pages and 100 items by default. A response
with `truncated: true` is partial. Use regular inbox pagination for a full list.
Use the receipt ID and its existing operation for review, results, or provider
reconciliation. Watching itself performs no workflow or provider operations.

The [CSV example](../examples/sdk-csv-jobs/README.md#watch-a-csv-imports-live-work-inbox)
shows the application routes, authenticated Node and Python watches, and a
reconnect after a missed refund update.

Use [typed inbox follow-ups](sdk-work-inbox-follow-ups.md) to continue a
selected entry from any snapshot.
