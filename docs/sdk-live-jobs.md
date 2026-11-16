# Live job and file-pipeline views (preview)

Applications can expose typed progress to browsers, Node agents and Python
agents using their existing private Gregale realtime endpoint. Existing Jobs
submit/status/wait/results and file-pipeline callers keep their behavior.

The application owns authentication, resource ownership and snapshots. Each
notification is a small invalidation (`progress`, `task_failed`, `completed`, or
`report_ready`). Its payload never changes a client's progress directly. The
client subscribes first, then reads an authenticated snapshot; it reads again
after reconnects, retained-history gaps and valid notifications. Delayed,
duplicate and reordered notifications therefore cannot replay old state.

Snapshots contain `version: 1`, `resource_id`, `run_id`, status, five task counts,
`failures: [{input_id, code}]`, and `report: none|pending|ready|unavailable`.
The SDK validates and copies only these fields. Resource IDs, run IDs, input IDs
and failure codes use 1–256 ASCII letters, digits, underscores, dots, colons or
hyphens. Counts must be safe nonnegative JSON integers, and their sum cannot
exceed total. A resource becomes bound to its first accepted run. A later
snapshot changing that run or regressing native terminal status fails closed.

`awaiting_storage_event` means no accepted run is present in the application
ledger yet. `submission_unresolved` keeps reading the receipt within the deadline:
another consumer may still be recording acceptance. A permanently unresolved
intent requires explicit run-ID reconciliation; it never submits another run.
The other states match
native Jobs. Native terminal state ends observation when the report is no
longer pending. `ready` requires terminal Jobs confirmation and a verified,
materialized application report. Worker-reported success is insufficient.

## Browser and Node

```ts
import {
  createLiveJob, realtimeLiveJobSource,
  createBrowserRealtimeTicketSocketFactory, RealtimeConfigurationError,
} from '@gregale/sdk-node/preview/browser';

// appJSON authenticates one captured application session, rejects 401/403/404
// permanently with RealtimeConfigurationError, and honors AbortSignal.
const initial = await appJSON(`/files/${importId}/live-ticket`);
const live = createLiveJob({
  resourceId: importId,
  timeoutMs: 120_000,
  signal: abort.signal,
  maxSnapshots: 1000,
  loadSnapshot: signal => appJSON(`/files/${importId}/snapshot`, {signal}),
  source: realtimeLiveJobSource({
    url: initial.url,
    channel: initial.channel,
    webSocketFactory: createBrowserRealtimeTicketSocketFactory(async () => {
      const next = await appJSON(`/files/${importId}/live-ticket`);
      if (next.url !== initial.url || next.channel !== initial.channel)
        throw new RealtimeConfigurationError('subscription scope changed');
      return next.ticket;
    }),
  }),
});
try {
  for await (const snapshot of live.updates()) renderProgress(snapshot);
} finally {
  live.close();
}
```

Node imports the same helpers from `@gregale/sdk-node/preview` and supplies a
WebSocket factory supporting its runtime. A controller is lazy and shared by
its listeners; its last listener leaving stops the source. Async iteration
keeps one latest view and coalesces intermediate notifications. It is a current
state view, not an audit stream that delivers every task transition. Deadlines,+cancellation, subscription errors and snapshot errors release the source and
fence late callbacks, including loaders that ignore cancellation. Errors expose
stable codes without arbitrary server/transport exception text.

React uses the same controller, with no separate connection per component:

```tsx
import {useLiveJob} from '@gregale/sdk-node/preview/react';
function ImportProgress({live}) {
  const {connection, snapshot, error} = useLiveJob(live);
  return <p>{error ?? `${connection}: ${snapshot?.succeeded ?? 0} rows saved`}</p>;
}
```

Create a new controller when the authenticated session changes. Close the old
one immediately and disable report downloads while reauthorizing. A controller
must never be reused across users, even if both users may access a resource.
The CSV example implements this behavior on token changes.

## Python agents

```py
from contextlib import aclosing
from faas_sdk.preview import LiveJob, RealtimeWebSocketClient, realtime_live_job_source

async def token():
    ticket = await app_json(f"/files/{import_id}/live-ticket")
    # Require the same endpoint/channel as the initial authorized ticket.
    if ticket["url"] != initial["url"] or ticket["channel"] != initial["channel"]:
        raise ValueError("Subscription scope changed")
    return ticket["ticket"]

async def snapshot():
    return await app_json(f"/files/{import_id}/snapshot")

transport = RealtimeWebSocketClient(url=initial["url"], token=token)
live = LiveJob(resource_id=import_id, load_snapshot=snapshot, timeout=120,
    source=realtime_live_job_source(connect=transport.connect, channel=initial["channel"]))
async with aclosing(live.updates()) as updates:
    async for state in updates:
        print(state.status, state.succeeded, state.report)
```

Install the existing Python realtime/WebSocket extra for the native transport.
Timeouts are seconds in Python and milliseconds in Node. Cancel the iterator's
task to stop locally; this does not cancel remote Jobs. Always close an iterator
when leaving early. Each Python iterator owns its subscription. Counts and the
snapshot contract match Node/browser; the shared JSON cases run in both suites.

## Backend setup and recovery

Use `issueRealtimeSubscriptionTicket` only after looking up the owned resource.
Choose its channel and principal on the server, bind tickets to that exact
channel, and return them with `Cache-Control: no-store`. Keep tickets only in
client memory. Renew authorization on every reconnect. Revoke the principal's
tickets with `revokeRealtimeSubscriptionTickets` when revoking an application
session or membership; rotating an application token alone does not revoke
previously issued tickets. Never let a browser request arbitrary channel names,
storage keys or native run IDs, and never forward platform credentials.

The native Jobs API currently needs server-side observation for aggregate
progress. `createLiveJobBridge` shares one observer per resource within a server
process. Its authenticated snapshot loader reads native Jobs and results; its
publisher sends retained invalidations using a stable key derived from the
entire snapshot. The bridge bounds concurrent resources, request duration,
polls and viewer leases. Renew a lease only after authorizing a new ticket.
Close the bridge on server shutdown. A closed browser's lease can remain active
until expiry. Multiple server replicas may observe and publish duplicates;
this is safe for current-state consumers but is not a global observer lease or
an exactly-once task notification guarantee.

The bridge is ephemeral. Durable receipts and native Jobs remain the recovery
source after a server restart, idle period, notification expiry or missed
publish. Every new/reconnected subscriber loads a fresh snapshot. Applications
requiring durable business side effects should use the existing transactional
outbox/event consumer helpers instead of acting on UI invalidations. No new
platform tables, broker or background daemon are introduced here.

See [the CSV example](../examples/sdk-csv-jobs/README.md#browser-file-imports-preview)
for upload → native storage event → typed job → live progress → authenticated
report download. Realtime retained delivery, v2 resume, subscription tickets,
storage and Jobs retain their existing preview, MFA and account gates. Local
protocol fixtures and Chrome checks do not establish deployed acceptance.
