# @gregale/sdk-node

> Node 22 SDK for the Gregale platform. Generated from
> [`api/openapi.yaml`](../../api/openapi.yaml), wrapped in a hand-written
> façade that ships retry, RFC 7807 error sentinels, idempotency, and SSE.

The SDK includes `verifyWebhook` for verifying signed outbound Gregale
webhook requests. See
[`docs/webhook-receiver-verification.md`](../../docs/webhook-receiver-verification.md)
for usage and delivery-ID deduplication guidance.

> **Heads-up: publish state.** The manifest name is now the conventional
> scoped form `@gregale/sdk-node`. It was previously `gregale` + `/skd-node`
> — a typo that npm rejects outright, since an unscoped name may not contain
> a slash. `private` is cleared and the `repository` field is set. `publishConfig.access` stays `restricted` until the licensing
> question in [`sdk/README-publishing.md`](../README-publishing.md) is
> settled — the current LICENSE is proprietary and non-redistributable.
> **Nothing is on the public npm registry yet.**
>
> **Heads-up: pre-1.0.** Version `0.1.0` is the pre-1.0 signal. The
> public API may shift between `0.x` releases. Pin to an exact version
> in your `package.json` until `1.0.0` ships.

## Requirements

- Node ≥ 22.10 for the Node SDK entry point (uses
  `--experimental-strip-types` at dev-time and global `fetch` at runtime).
- The browser subpath uses the standard Fetch API and has no Node-only runtime
  imports.
- npm ≥ 10 (or `pnpm`/`yarn` compatible).

## Install

Once the package is published:

```sh
npm install @gregale/sdk-node
```

**Today it is not published yet**, so install from a locally built tarball.
npm cannot install a package from a subdirectory of a git repo, so
`npm install github:poyrazK/faas#<tag>` does *not* work — the repo root has
no `package.json`, and `dist/` is a build artifact that is not committed.

```sh
git clone https://github.com/poyrazK/faas.git
cd faas/sdk/node
npm ci && npm run build
npm pack --pack-destination /tmp     # -> /tmp/gregale-sdk-node-0.1.0.tgz

cd /path/to/your/project
npm install /tmp/gregale-sdk-node-0.1.0.tgz
```

## Container listeners

UDP ingress requires an operator-enabled public edge and source-CIDR/firewall
rollout. The app must declare the guest UDP port. Reserving a listener creates a
disabled endpoint; enable it explicitly after deployment and rollout checks:

```ts
import { FaaSClient, AppsService } from '@gregale/sdk-node';

new FaaSClient('https://api.example.com', { token: process.env.FAAS_TOKEN! });
const udp = await AppsService.createAppUdpListener({
  slug: 'app', requestBody: { name: 'dns', guest_port: 5353 },
});
await AppsService.updateAppUdpListener({
  slug: 'app', name: udp.name, requestBody: { enabled: true },
});
```

An existing TCP listener can terminate TLS using a verified app-owned hostname.
The operator provisions its certificate bundle on the serving edge. Updating TLS
policy disables the listener; send a separate enable mutation after provisioning:

```ts
await AppsService.updateAppTcpListener({
  slug: 'app', name: 'echo',
  requestBody: { tls: { mode: 'terminate', hostname: 'echo.example.com' } },
});
await AppsService.updateAppTcpListener({
  slug: 'app', name: 'echo', requestBody: { enabled: true },
});
const status = await AppsService.appTcpListenerTlsStatus({ slug: 'app', name: 'echo' });
console.log(status.observations);
```

Supply exactly one of `enabled` or `tls` in each TCP update. Status covers observed
edges only: empty observations or `unknown` do not establish readiness. Certificate
readiness does not prove fleet coverage, client trust or guest availability.
Native listener qualification remains pending; see the
[qualification procedure](../../docs/container-qualification.md).

## Quick start

```ts
import { FaaSClient, AppsService, ErrNotFound } from '@gregale/sdk-node';

const client = new FaaSClient('https://api.example.com', {
  token: process.env.FAAS_TOKEN!,
  retry: { maxAttempts: 3, backoffMs: 100 },
});

try {
  const app = await AppsService.getApp({ slug: 'hello' });
  console.log(app.url);
} catch (err) {
  if (err instanceof ErrNotFound) {
    console.warn('app does not exist');
  } else {
    throw err;
  }
}
```

The four canonical error sentinels (`ErrNotFound`, `ErrUnauthorized`,
`ErrRateLimited`, `ErrCapacity`) all extend `FaasError` and carry the
parsed RFC 7807 `Problem` envelope, the HTTP status, and the daemon's
`tx_id` for support tickets.

## Supported surface

### Resumable managed realtime preview

`consumeRealtimeChannel` processes one v2 channel and reconnects from its
cursor. Use `cursorStore` to keep that cursor in your application, or use
`durableSubscription` to let Gregale persist it for a stable authenticated
principal. Supply a WebSocket factory that adds the endpoint's OIDC bearer
token. For example, with the separate `ws` package
(`npm install ws` and `npm install -D @types/ws` for TypeScript):

```ts
import WebSocket from 'ws';
import { consumeRealtimeChannel } from '@gregale/sdk-node';

await consumeRealtimeChannel({
  url: 'wss://app.example.com/__gregale/realtime/ENDPOINT_ID',
  channel: 'notifications',
  cursorStore: {
    load: async () => Number(await cursorDB.get('notifications') ?? 0),
    save: async (sequence) => { await cursorDB.set('notifications', sequence); },
  },
  onMessage: async ({ sequence, data }) => {
    await processNotification(sequence, data); // make this idempotent by sequence
  },
  onResync: async (gap) => {
    const snapshot = await rebuildNotificationState({
      channel: gap.channel,
      throughSequence: gap.latestSequence,
    });
    await replaceNotificationState(snapshot.state);
    return snapshot.sequence; // fully represented by the rebuilt state
  },
  webSocketFactory: async (url, protocols) => new WebSocket(url, protocols, {
    headers: { Authorization: `Bearer ${await getFreshOidcToken()}` },
  }),
});
```

For server-managed progress, replace `cursorStore` with a stable name for this
logical consumer. Use a different name for each device that must receive every
message independently:

```ts
await consumeRealtimeChannel({
  url: 'wss://app.example.com/__gregale/realtime/ENDPOINT_ID',
  channel: 'notifications',
  durableSubscription: 'phone-install-7',
  onMessage: async ({ messageId, data }) => {
    await processNotificationOnce(messageId, data);
  },
  webSocketFactory: async (url, protocols) => new WebSocket(url, protocols, {
    headers: { Authorization: `Bearer ${await getFreshOidcToken()}` },
  }),
});
```

For a newly named consumer that already has an application snapshot, set
`initialSequence` to the snapshot's sequence so it starts after represented
messages. Existing server checkpoints take precedence; an omitted baseline
starts at sequence 0. If retained history no longer covers that baseline, the
consumer requests a resync.

The helper calls `onMessage`, then sends the ack. With `cursorStore`, it saves
the cursor before sending the ack. With `durableSubscription`, Gregale stores
the checkpoint after receiving the ack. If processing or cursor saving fails,
it stops without advancing. A crash between the application side effect and
acknowledgement can cause redelivery, so deduplicate using the channel and
sequence. When possible, store that deduplication key with the application
side effect in one transaction.
An expired cursor calls `onResync` with the channel, stale cursor, and retained
history bounds. Return a cursor between `oldestSequence - 1` and
`latestSequence` that the rebuilt state fully represents; the helper saves it
locally or asks Gregale to reset the named subscription before reconnecting.
Omit `onResync` to receive `RealtimeResyncRequiredError`; the stored cursor
remains unchanged until the consumer is restarted with a recovery callback.
Cancel with an `AbortSignal` to stop reconnecting.
Named subscriptions share progress when they use the same endpoint, principal,
name, and channel, so use distinct names for devices that need independent
delivery. Gregale allows 256 named cursor rows per endpoint and removes rows
after 30 days without activity. The bounded message history can expire first.
This preview requires both server preview flags and the endpoint's channel
authorization callback described in
[managed realtime operations](../../docs/ops/realtime.md).

The v2 consumer can also maintain fleet-wide presence and send short-lived
signals to authorized v2 clients in the channel. These events are not retained
and do not advance the message cursor. `onSubscribed` runs after every
reconnect and gives you actions for the current connection:

```ts
import WebSocket from 'ws';
import { consumeRealtimeChannel, type RealtimeChannelActions } from '@gregale/sdk-node';

let room: RealtimeChannelActions | undefined;
function onTypingChanged(typing: boolean) {
  room?.updatePresence({ status: typing ? 'typing' : 'online' });
}
function onCursorMove(x: number, y: number) {
  room?.sendSignal({ kind: 'cursor', x, y });
}

const consumer = consumeRealtimeChannel({
  url: 'wss://app.example.com/__gregale/realtime/ENDPOINT_ID',
  channel: 'room-a',
  cursorStore: roomCursorStore,
  presence: { status: 'online' },
  presenceScope: 'principal',
  onMessage: async (message) => applyRoomEvent(message),
  onSubscribed: (actions) => { room = actions; },
  onPresence: (event) => updateRoomMembers(event),
  onSignal: (signal) => handleRoomSignal(signal),
  onDirectMessage: ({ data, binary }) => handleDirectNotification(data, binary),
  webSocketFactory: async (url, protocols) => new WebSocket(url, protocols, {
    headers: { Authorization: `Bearer ${await getFreshOidcToken()}` },
  }),
});

// Wire these functions to UI events before awaiting the long-running consumer.
await consumer; // runs until the supplied AbortSignal is aborted
```

Presence objects are limited to 512 UTF-8 bytes and 10 updates per second.
Signals accept JSON up to 2 KiB and are limited to 20 per second. The channel
limit is 50 per second per sending node. The `onPresence` snapshot can arrive
in chunks; `complete: true` marks its final chunk. By default, presence
membership uses one opaque `memberId` per connection. Set
`presenceScope: 'principal'` to group connections that share the verified OIDC
principal into one opaque member; `connectionCount` shows how many sockets it
represents. The principal itself is never sent to clients. For grouped members,
the most recently changed state wins across devices; keep the default connection
scope when each device needs separate state. Principal scope requires a verified
principal and fleet presence storage. Presence state is client supplied, not a
verified identity. Presence leases expire after 45 seconds if a node fails and
are renewed every 15 seconds, with a limit of 512 active connections per
channel. Signal delivery is best-effort; the optional
`onEphemeralRejected` callback reports rate limits and relay failures.

`onDirectMessage` opts the connection into live-only backend messages addressed
to its verified principal. The callback receives an optional `messageId`,
binary-safe `Uint8Array` data, a `binary` flag, and `receiptRequested`. When a
sender requested a receipt, the message ID is present and the SDK acknowledges
after the callback resolves, so the callback should resolve only after the
application has handled the message. These messages do not belong to a channel
history and are not replayed after reconnect.

For notifications that must survive a disconnect, have the backend use
`send-principal --delivery retained --message-id ID` and consume the authenticated
principal inbox. Give each independently consuming device a stable `consumerId`:

```ts
import { consumeRealtimeInbox, createBrowserRealtimeSocketFactory } from '@gregale/sdk-node/browser';

await consumeRealtimeInbox({
  url: realtimeEndpointURL,
  webSocketFactory: createBrowserRealtimeSocketFactory(() => session.getOIDCToken()),
  signal: abortController.signal,
  inbox: {
    consumerId: persistedDeviceID,
    onMessage: async ({ messageId, sequence, data }) => {
      await saveNotificationOnce(currentUserId, messageId, sequence, data);
    },
    onResync: async ({ latestSequence }) => {
      await reloadNotificationsFromBackend();
      return latestSequence;
    },
  },
});
```

The inbox callback runs before the SDK sends an acknowledgement. Checkpoints
are stored on the server per endpoint, principal, and consumer ID. On reconnect,
the server checkpoint takes precedence over `initialSequence` (default zero for
a new consumer). Delivery is at least once; a lost acknowledgement can replay a
message, so commit side effects idempotently. Scope deduplication keys to the
endpoint and authenticated principal. A device may share the same socket
with channels by setting `inbox` on `consumeRealtimeChannels`; the inbox counts
as one of the eight allowed subscriptions.

Inbox messages are retained for up to 24 hours and the most recent 256 messages
per principal. An expired cursor triggers `onResync` and an explicit checkpoint
reset. Without that hook, the SDK throws `RealtimeInboxResyncRequiredError`.
Device checkpoints expire after 30 days of inactivity. Use a distinct persistent
consumer ID for each device that needs independent replay progress. These
features require the retained-message API preview and realtime v2 preview.

Use `consumeRealtimeChannels` to multiplex up to eight subscriptions on one
WebSocket (including an optional inbox). Each channel has its own client cursor store or durable subscription
name (with an optional initial sequence), message handler, and optional resync
callback; the connection URL, token factory, retry policy, and abort signal
are shared:

```ts
import WebSocket from 'ws';
import { consumeRealtimeChannels } from '@gregale/sdk-node';

await consumeRealtimeChannels({
  url: 'wss://app.example.com/__gregale/realtime/ENDPOINT_ID',
  channels: [
    {
      channel: 'jobs',
      cursorStore: jobCursorStore,
      onMessage: async (message) => updateJobProgress(message),
    },
    {
      channel: 'notifications',
      cursorStore: notificationCursorStore,
      onMessage: async (message) => processNotification(message),
    },
  ],
  webSocketFactory: async (url, protocols) => new WebSocket(url, protocols, {
    headers: { Authorization: `Bearer ${await getFreshOidcToken()}` },
  }),
});
```

Handlers run sequentially in received frame order, so slow processing applies
backpressure to every channel on that socket. A resync on one channel saves or
resets that channel's cursor and reconnects the shared socket; the other
channels resume from their own cursors and can receive duplicates under the
at-least-once delivery model. `consumeRealtimeChannel` remains available for a
single channel and uses the same implementation.

Backend publishers can pass a stable key to the generated API service. Derive
it once from the logical event or outbox row, and reuse it only when retrying
that exact payload:

```ts
import { RealtimeService } from '@gregale/sdk-node';

const payload = Buffer.from(JSON.stringify(event));
const outcome = await RealtimeService.publishManagedRealtimeChannel({
  slug: 'my-app',
  id: 'ENDPOINT_ID',
  channel: 'jobs',
  idempotencyKey: `job-event:${event.id}`,
  requestBody: { data_base64: payload.toString('base64') },
});
```

The server replays the original outcome for 24 hours. A replay of a partial
publish does not retry subscribers that missed it; use a new key for a new
attempt only when possible duplicates are acceptable. A key reused with a
different payload or delivery mode returns `409`.

To make a message resumable, opt into retained delivery. It requires the apid
retained-history preview flag, the realtimed resume preview flag, a stable key,
and a payload no larger than 4 KiB. The response includes the durable channel
sequence; v2 subscribers read messages in that order, while raw-frame clients
receive the normal live publish:

```ts
const retained = await RealtimeService.publishManagedRealtimeChannel({
  slug: 'my-app',
  id: 'ENDPOINT_ID',
  channel: 'jobs',
  delivery: 'retained',
  idempotencyKey: `job-event:${event.id}:retained`,
  requestBody: { data_base64: payload.toString('base64') },
});
console.log(retained.sequence, retained.durable);
```

Browser clients import from the browser subpath. The server accepts a bounded
OIDC JWT in a reserved WebSocket subprotocol when the endpoint has an explicit
`allowed_origins` entry matching the page's origin:

```ts
import {
  consumeRealtimeChannel,
  createBrowserRealtimeSocketFactory,
} from '@gregale/sdk-node/browser';

const cursorKey = `realtime:ENDPOINT_ID:${currentUser.id}:notifications`;
await consumeRealtimeChannel({
  url: 'wss://app.example.com/__gregale/realtime/ENDPOINT_ID',
  channel: 'notifications',
  cursorStore: {
    load: () => Number(localStorage.getItem(cursorKey) ?? 0),
    save: (sequence) => { localStorage.setItem(cursorKey, String(sequence)); },
  },
  onMessage: async ({ sequence, data }) => {
    await processNotification(sequence, data); // deduplicate by channel and sequence
  },
  webSocketFactory: createBrowserRealtimeSocketFactory(getFreshOidcToken),
});
```

The factory fetches a fresh JWT on every reconnect. For project release
pinning, pass `createGregaleBrowserFetch(...).webSocket` as its second argument.
The browser cursor is scoped to the current user; if browser storage is cleared
or evicted, the application may need to rebuild state and save a fresh cursor.
The JWT travels in the `Sec-WebSocket-Protocol` request header during the
handshake. Keep it short lived and redact that header from proxy access logs.
The server verifies the JWT, requires an exact allowed origin, removes the
credential before application authorization hooks run, and selects only
`gregale.realtime.v2` as the response subprotocol.

Server-side Node services can also use the hand-written
`createServiceCallerVerifier` helper to verify Gregale's incoming internal
service-call assertions. It uses the platform public JWKS endpoint and Node's
built-in Ed25519 support. See [the networking guide](../../docs/networking.md#verifying-the-caller-preview)
for setup, rollout requirements, and an HTTP handler example. Verification
authenticates the caller but does not replace the target's caller allowlist or
business authorization.

Every operation in `api/openapi.yaml` is reachable through the
generated services. The canonical mapping:

| OpenAPI tag | Generated service |
|---|---|
| `account` | `AccountService` |
| `apps` | `AppsService` |
| `audit` | `AuditService` |
| `auth` | `AuthService` |
| `crons` | `CronsService` |
| `delayed_tasks` | `DelayedTasksService` |
| `deployments` | `DeploymentsService` |
| `domains` | `DomainsService` |
| `github` | `GithubService` |
| `instances` | `InstancesService` |
| `invocations` | `InvocationsService` |
| `keys` | `KeysService` |
| `meta` | `MetaService` |
| `mfa` | `MfaService` |
| `queues` | `QueuesService` |
| `runs` | `RunsService` |
| `secrets` | `SecretsService` |
| `usage` | `UsageService` |

Regenerate via `npm run gen` (committed per ADR-013; CI's
`sdk-gen-node` job is the dirty-diff gate).

## Transactional operation handlers

Customer Operations HTTP definitions explicitly enable
`transaction_receipt: postgres_v1` with reconciliation recovery. Use
`customerOperationReceiptRequestFromHeaders` and
`withCustomerOperationReceiptTransaction`;
the callback returns the ordinary JSON result, without managed effects.
Approved recovery checks a scoped receipt before business code. Install and
retain `customerOperationReceiptSchema` as the application database owner.
See [Customer Operations transaction adapter](../../docs/operation-transactions.md#customer-operations-http-adapter).

For managed HTTP operations, `operationRequestFromHeaders` verifies negotiated
support and captures the trusted identity with original request bytes.
`withOperationTransaction(pool, operation, callback)` commits the callback's
PostgreSQL writes and managed result/webhook intent together. A later attempt
returns the saved response without repeating committed writes.

Install `operationReceiptSchema` explicitly as the database owner. Use an idle,
exclusively leased pg-compatible pool connection. Send `response.body` unchanged
as `application/json`; `response.replayed` identifies receipt recovery. See the
[transactional handler guide](../../docs/operation-transactions.md) for Express,
receipt retention, and uncertain commit handling.

## Idempotency contract

Every mutating call (POST/PUT/PATCH/DELETE) carries an `Idempotency-Key`
header. The semantic contract is identical to the Go SDK:

- **Auto-mint (default).** The wrapper mints a fresh UUIDv4 on **every
  attempt**, not per-call. This means each retry sees a fresh key and
  the server's 24h replay window sees a fresh retry budget per attempt;
  a retried `CreateApp` is a new logical request from the server's
  perspective, never a double-bill.
- **Opt-in stable key.** For deployments that need to survive across
  processes (CI deploys, retry-batched jobs), pin a stable key so the
  server replays the original response on retry rather than minting
  new logic:

  ```ts
  client.setIdempotencyKey('deploy-2026-07-26-batch-7');
  await AppsService.createApp({ requestBody: { slug: 'foo' } });
  // Subsequent retries of the same logical operation will receive
  // the same response from the server's 24h replay window.
  ```

  The key is process-wide on the `FaaSClient` instance and persists
  until reset. Pass a fresh key per logically independent call.

GET/HEAD skip the header — the server doesn't dedupe reads.

The `client.setIdempotencyKey` API is the only public stable-key wire-in
in PR 5. A future AsyncLocalStorage-based per-call key (PR 11 if
docs customers request it) would layer on top without breaking the
existing contract.

## Login-target observation

For a `POST` login route configured with `failed_responses`, central
coordination, and `observe_targets: true`, attach an opaque target to each
selected failed response. Use the exact normalization applied during account
lookup for both existing and unknown accounts:

```ts
import { PRE_AUTH_TARGET_HEADER, preAuthTargetDigest } from '@gregale/sdk-node';

const targetKey = process.env.GREGALE_ABUSE_TARGET_KEY;
if (!targetKey) throw new Error('GREGALE_ABUSE_TARGET_KEY is required');

function failedLoginResponse(normalizedIdentifier: string): Response {
  return new Response('Invalid credentials', {
    status: 401,
    headers: { [PRE_AUTH_TARGET_HEADER]: preAuthTargetDigest(targetKey, normalizedIdentifier) },
  });
}
```

Create a random key of at least 32 bytes, keep it server-side, and share it
across replicas. The helper takes an already-normalized identifier and returns
a lowercase HMAC-SHA256 digest. It does not decide which responses are login
failures. Attach the header exactly once only on failed responses. Gregale
removes it before returning the response to the client. See
[pre-auth security guidance](../../docs/security.md) for tenant-scoped
identifiers and key rotation. Call `failedLoginResponse` with the normalized
lookup value after either an unknown account or an incorrect credential.

## Dev Bridge request context

Opt a remote HTTP service into preserving a developer's routing context across
managed service calls. In an Express service, install `devBridgeMiddleware` before
handlers and use `createDevBridgeFetch` for outbound HTTP:

```ts
import { devBridgeMiddleware, createDevBridgeFetch } from '@gregale/sdk-node';

app.use(devBridgeMiddleware);
const serviceFetch = createDevBridgeFetch();
const paymentsURL = process.env.GREGALE_SERVICE_PAYMENTS_URL;
if (!paymentsURL) throw new Error('Declare the payments service binding');
app.get('/charge', async (_request, response) => {
  const result = await serviceFetch(paymentsURL + '/charge');
  response.status(result.status).send(await result.text());
});
```

For a framework using Fetch headers, wrap its handler with
`withDevBridgeRequestContext(request.headers, handler)`. AsyncLocalStorage keeps
concurrent developer and ordinary requests separate. The wrapper removes explicit
bridge credentials on every destination and propagates request context only to
single-label `NAME.svc.gregale` or `NAME.internal` discovery names. Gregale still
authorizes the lease at each hop. Application `Authorization` is preserved.

Scoped managed requests use manual redirects: inspect `Location` and call the
wrapper again if the application chooses to follow it. Do not hand the scoped
request to an unwrapped fetch that automatically follows redirects. The helpers
are Node-only; the browser subpath does not expose laptop/session authority.
See [the Dev Bridge guide](../../docs/dev-bridge.md) for local execution.

## Project release context

Capture the release selected for an inbound Gregale request and use the
wrapped fetch for outbound managed service calls. The helper forwards only
`X-Gregale-Release` to `*.svc.gregale` and removes the caller-scoped
`X-Gregale-Revision` header on that hop:

```ts
import { createGregaleFetch, withGregaleRequestContext } from '@gregale/sdk-node';

const serviceFetch = createGregaleFetch();

async function checkout(request: Request) {
  return withGregaleRequestContext(request.headers, async () => {
    return serviceFetch('http://billing.svc.gregale:10080/checkout', { method: 'POST' });
  });
}
```

The async context is isolated between concurrent handlers. Use it only around
work caused by that inbound request; detached background jobs should capture
the release explicitly when they are enqueued.

### Browser SPA release pinning

For an SSR-rendered document, put the release selected on the inbound page
request into the HTML before sending it. Gregale has already resolved the
active release and forwarded it to the app as `X-Gregale-Release`:

```ts
import { gregaleReleaseMetaTag } from '@gregale/sdk-node';

function renderPage(request: Request): Response {
  const releaseMeta = gregaleReleaseMetaTag(request.headers);
  const html = `<!doctype html>
<html>
  <head>${releaseMeta}</head>
  <body><div id="app"></div><script type="module" src="/app.js"></script></body>
</html>`;
  return new Response(html, { headers: { 'Content-Type': 'text/html; charset=utf-8' } });
}
```

The helper emits nothing when the request has no valid release ID, and only
emits the UUID form accepted by the gateway's release-pin API. Since this
value is request-specific, shared caches for rendered HTML must be disabled or
keyed by `X-Gregale-Release`.

Browser clients can use the browser-safe fetch adapter without importing the
Node-only SDK entry point:

```ts
import { createGregaleBrowserFetch } from '@gregale/sdk-node/browser';

const releaseFromBootstrap = document
  .querySelector('meta[name="gregale-release"]')
  ?.getAttribute('content') ?? undefined;
const gregale = createGregaleBrowserFetch({
  managedOrigins: ['https://api.example.com'],
  // Prefer the release that served this app when it is available.
  initialRelease: releaseFromBootstrap,
});

const response = await gregale.fetch('https://api.example.com/v1/checkout', {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ cartId: 'cart-123' }),
});
```

The adapter adds `X-Gregale-Release` only to configured Gregale origins,
captures it from the first eligible response when no initial release was
provided, and pins later calls from that adapter instance. It serializes
concurrent unpinned startup calls while discovering the release. The
discovery request itself follows the active release, so inject `initialRelease`
from the HTML/SSR/bootstrap response when the API must match the exact release
that served the client. For static SPAs, Gregale sets the host-only,
JavaScript-readable `__Host-gregale_release` cookie on a document navigation
when a release graph is selected. The browser adapter reads that cookie when
created and uses it as the initial release, including for configured
cross-origin API origins. The cookie is a routing identifier, not a secret;
Gregale removes it before forwarding requests to the guest. Apps must have
revision pin retention enabled, and any cache in front of the app must preserve
the document response's `Set-Cookie` header with its body. It is a browser
session cookie; the release graph's server-side TTL controls whether its value
is still routable.

Same-host browser WebSocket reconnects use the same bootstrap cookie without a
custom header: the native `WebSocket` API does not expose request headers. The
gateway reads the cookie only on a WebSocket handshake, routes to that release
if it remains eligible, and strips the platform cookie before the guest sees
the request. Because `__Host-` cookies are host-only, this automatic browser
behavior applies when the SPA and WebSocket endpoint share a hostname.

For a WebSocket endpoint on a separate managed API hostname, use the adapter's
`webSocket` helper after seeding or discovering the release:

```ts
const socket = gregale.webSocket('wss://api.example.com/events', ['graphql-transport-ws']);
```

The helper appends a reserved `Sec-WebSocket-Protocol` token carrying the
release UUID. Gregale consumes it before the application handshake, preserves
your application protocols, and removes any guest attempt to negotiate the
reserved token back to the browser. The token is a routing identifier, not a
secret; it is not placed in the URL. The endpoint origin must be listed in
`managedOrigins`. If the adapter has no release yet, `webSocket` throws rather
than opening an unpinned socket; seed `initialRelease` from SSR/bootstrap or
make a managed fetch first. Ordinary browser `new WebSocket(...)` calls do not
automatically pin cross-host endpoints.

State is in-memory per adapter instance after initialization; create a new
instance for a new client session. A 410 expired-release response is returned
unchanged and is never retried against the active release. Call `clearRelease()`
only when the application intentionally wants to start a new release context;
it also clears the bootstrap cookie for the current host.

For cross-origin APIs, configure CORS to expose `X-Gregale-Release` and allow
it as a request header. The default CORS policy already exposes both release
and revision response headers.

This SSR bootstrap binds the browser to the release that served its document.
A static HTML file served without request-time rendering cannot read the
navigation response headers from JavaScript; it must provide a release-specific
bootstrap value during publishing, or use a dynamic document/bootstrap route.
Without a seeded value, the adapter can only learn the active release from its
first API response and pin subsequent calls.

## Execution streaming

Disposable executions expose a typed, resumable iterator. It consumes output
as it is emitted and reconnects with the latest event cursor if the SSE
connection drops:

```ts
for await (const event of client.watchExecution(execution.id)) {
  if (event.type === 'stdout' || event.type === 'stderr') {
    process.stdout.write(event.data.chunk ?? '');
  }
  if (event.type === 'terminal') console.log(event.data.status);
}
```

`RunsService.streamExecutionEvents()` remains available when a raw SSE body
is needed. `watchExecution` is the recommended agent-runtime path.

For the common submit-and-wait flow, `runExecution` composes create, resumable
watching, and the terminal receipt:

```ts
const receipt = await client.runExecution(
  { runtime: 'node22', source: "console.log('hello')" },
  {
    onEvent: (event) => {
      if (event.type === 'stdout') process.stdout.write(event.data.chunk ?? '');
    },
  },
);
```

The source/files bundle is available only in the disposable guest's ephemeral
scratch filesystem; no customer storage disk is attached.

## SSE streaming

`/v1/logs/{app_id}/tail` (and a few other out-of-spec streams) expose
`text/event-stream`. Use `streamSse` for those raw streams:

```ts
import { streamSse } from '@gregale/sdk-node';

const resp = await fetch(`${client.baseURL}/v1/logs/hello/tail`, {
  headers: { Authorization: `Bearer ${process.env.FAAS_TOKEN}` },
});
for await (const ev of streamSse(resp, signal)) {
  console.log(ev.event, ev.data);
}
```

The parser handles the canonical SSE wire shape (LF or CRLF, comment
lines starting with `:`, multi-line `data:` joined with `\n`, unknown
fields ignored, `id:` + `retry:` surfaced). See
[`test/sse.test.ts`](./test/sse.test.ts) for the parsing contract.

`streamSse` honours the caller's `AbortSignal` for read cancellation
but does **not** layer a timeout — the caller owns the deadline. Pass
`AbortSignal.timeout(ms)` if you need a hard cap.

## Co-tenant code: the global fetch wrapper

The `FaaSClient` constructor replaces `globalThis.fetch` with the
wrapper chain (retry → RFC 7807 unwrap → logger → idempotency →
user fetch). This is the only injection point the
`openapi-typescript-codegen@0.31.0` generator exposes — see
`src/generated/core/request.ts:219`. **All other `fetch` calls in your
process see the wrapper too.**

Implications for callers:

- The wrapper is installed for the entire Node process, not just for
  requests routed through the SDK services. `fetch('https://other.example/')`
  in your code will also pass through the retry + idempotency stack.
- The `rfc7807Layer` raises typed `FaasError` sentinels on
  Problem-shaped bodies. If your own code calls `fetch` and inspects
  4xx/5xx responses, a 404 from your own endpoint will now throw
  `ErrNotFound` instead of returning a `Response` object. Two ways
  to mitigate:
  1. Pass a dedicated `fetch` via `FaaSClientOptions.fetch` — this
     bypasses the global entirely.
  2. Construct the `FaaSClient` only when you actually need it (e.g.
     inside a request handler), and call `uninstall()` on teardown.
- Two `FaaSClient` instances in the same process will clobber each
  other's wrapper. The library is designed for one `FaaSClient` per
  process (typical usage: module-level singleton).

Test rigs that mock `fetch` should pass `fetch: mockFn` via
`FaaSClientOptions` rather than setting `globalThis.fetch` directly —
the wrapper will chain the mock in the correct order.

## Zero runtime dependencies

`dependencies: {}` — the wrapper uses only Web APIs (`fetch`,
`AbortController`, `Headers`, `URL`) plus Node 22 built-ins
(`node:crypto`, `node:test`, `node:child_process`, `node:net`).

The `devDependencies` pin `openapi-typescript-codegen@0.31.0` and
`typescript@5.6.3`. Major-version bumps require an ADR.

## CI

The `sdk-gen-node` job in `.github/workflows/ci.yml` runs
`npm ci && npm run gen:check` (regen + dirty-diff assert). The
`sdk-smoke-node` job builds the fakeapid fixture, runs `npm run
test:smoke`, and tears down. The `sdk-unit-node` job runs the
in-process unit tests (`sse.test.ts`, `post-process.test.mjs`) which
don't require the fixture.

## License

Internal — see `LICENSE`.
## Internal HTTP Operations preview

Operations is staged and production submission remains disabled. For locally
enabled acceptance, import `GregaleOperationClient` from the browser entry point
and supply a callback that obtains a current tenant-bound token:

```ts
import { GregaleOperationClient } from '@gregale/sdk-node/browser';

const operations = new GregaleOperationClient({
  apiURL: 'https://api.example.com',
  credential: () => session.currentTenantToken(),
});
const receipt = await operations.start(definitionID, { count: 100 }, submissionKey);
for await (const update of operations.subscribe(receipt.id, { signal })) {
  await saveAndRender(update);
}
```

Keep `submissionKey` stable for duplicate submissions. Account API keys stay on
the backend. The client refreshes credentials on reconnect; persist the applied
event cursor and pass it as `after` when rebuilding the client.

Server handlers import `GregaleOperations` from the main entry point and wrap
trusted Gregale guest requests with `runRequest(req.headers, handler)`. Within
that handler, use `progress({ report_id, stage, completed, total })` and
`artifact({ report_id, name, uri, size_bytes, sha256 })`. Use stable report IDs
when repeating a report. Workload metadata is fetched for every report, while
the current invocation capability stays private to its request context.
See [Operations](../../docs/operations.md) for ownership, retention and recovery.

Account operators can call `OperationsService.inspectOperationRecovery` and
`previewOperationRecovery` to inspect retained execution evidence and a proposed
resolution. Preview starts no work or file publication. Its `eligible` field
describes platform checks; operator evidence must still establish external effects.
Pass the returned `inspection_revision` as `expected_inspection_revision` in a
separate `recoverOperation` request to reject changed execution evidence.

For direct private HTTP files, use a stable report ID inside the original request:

```ts
const output = await runtime.runCancellableRequest(req.headers, async () => {
  const artifact = await runtime.uploadArtifact({
    report_id: 'export-file', name: 'export.csv', data: csv, maxBytes: 32768,
  });
  return {artifact_id: artifact.id, rows: count};
});
```

No bucket writer or storage credential is needed. The helper snapshots text or
bytes, computes size/SHA-256 and coalesces matching calls. It checks a retained
receipt before each of at most three private transport attempts, fetching fresh
workload identity each time. Conflicting declarations and invalid receipts stop
without retrying business code. The default SDK memory bound is 8 MiB; the API
also enforces captured quotas. Cooperative cancellation aborts I/O, and uploads
cannot outlive their original request. Retaining a file never completes HTTP
work; downloads require a successful operation or explicit success recovery.
The [HTTP export starter](../../examples/customer-operation-export/README.md)
uses the exact `artifact_id` from its typed result for the private download.

Managed source files remain supported. For file results, `prepareArtifact` snapshots bounded text or bytes, computes the
exact UTF-8 byte count and `sha256:` digest, and keeps one immutable report ID:

```ts
import { GregaleOperations } from '@gregale/sdk-node/operations/runtime';

await runtime.runRequest(req.headers, async () => {
  const context = runtime.context()!;
  const file = runtime.prepareArtifact({
    report_id: 'export-file', name: 'export.csv', data: csv,
    uri: `obj://${appID}/${bucketID}/exports/${context.id}/${context.attempt}.csv`,
    maxBytes: 8 * 1024 * 1024,
  });
  await file.uploadAndAttach(async ({ report, bytes }) => {
    await resultStore.write(report.uri, bytes); // Your existing bucket writer.
  });
});
```

For cooperative cancellation and deadlines, opt into a request scope:

```ts
const output = await runtime.runCancellableRequest(req.headers, async scope => {
  await scope.checkpoint();
  const csv = await generateCSV({ signal: scope.signal, checkpoint: scope.checkpoint });
  scope.throwIfStopped();
  const context = runtime.context()!;
  const file = runtime.prepareArtifact({
    name: 'export.csv', data: csv, maxBytes: 8 * 1024 * 1024,
    uri: `obj://${appID}/${bucketID}/exports/${context.id}/${context.attempt}.csv`,
  });
  await scope.checkpoint();
  const attached = await file.uploadAndAttach(({ report, bytes }) =>
    resultStore.write(report.uri, bytes, { signal: scope.signal }));
  return { artifact_id: attached.artifacts![0]!.id };
});
res.json(output); // Send the response after the final control check.
```

The scope provides a signal, the admitted `deadlineAt`, a fresh-control
`checkpoint()` and a synchronous `throwIfStopped()` for CPU work. Yield between
chunks so polling can run. It reads control before business code and again before
returning output, fetches fresh workload identity on every poll, and aborts on
cancellation, deadline, lease expiry or lost control. Polls do not consume report
quota or renew authority. Cleanup aborts pending control I/O and clears timers.
`OperationStoppedError.code` identifies the local stop reason without exposing
remote errors. `runtime.control()` also supports a manual typed observation.

This is cooperative: ignoring the signal can leave work running. Server time
durations and request latency bound the local budget without trusting the
client's absolute clock; forward clock steps shorten it and backward steps
cannot extend it. An observation cannot fence an external effect atomically.
Stopping preserves uncertainty under the definition's existing recovery policy;
it does not undo an upload, mark the operation safely cancelled or retry work.

The writer uses the application's existing bucket binding and credentials. Use
an operation/attempt-specific source key in a private bucket belonging to this
app and environment. The helper preserves opaque object keys and accepts managed
`obj://` references, never signed URLs. `maxBytes` bounds the application's memory
copy; server plan quotas, ownership checks and private retention still apply.
An attachment report alone does not complete the business operation.

Keep the prepared object inside its original `runRequest` callback. Concurrent
calls share one transfer. It invokes the writer at most once, even if the write
response is lost. Call `file.attach()` or repeat `file.uploadAndAttach(writer)` to
replay the same report without another write. Gregale verifies an existing source
or returns its prior retained receipt. If the source is absent or mismatched,
the error remains unresolved; inspect storage before explicitly authorizing any
new write. Successfully attached receipts are cached within this prepared object.
There is no cross-process receipt persistence or automatic handler retry.

For a complete feature, initialize the CLI starter:

```sh
gregale init --template customer-operation-export --path customer-operation-export
```

Its README explains installing an internal SDK tarball and running the generated
tests without relying on a public registry. The starter uses the dedicated
browser-safe `@gregale/sdk-node/operations` entry point; handlers can import
`GregaleOperations` from `@gregale/sdk-node/operations/runtime`.

`GregaleOperationSession<TOutput, TInput>` supplies framework-neutral feature
state. Its first type is the result; its second types explicit submissions:

```ts
import { GregaleOperationClient, GregaleOperationSession, createBrowserOperationReceiptStore } from '@gregale/sdk-node/operations';

type ExportInput = { count: number };
type ExportResult = { csv: string };

const feature = new GregaleOperationSession<ExportResult, ExportInput>({
  client: new GregaleOperationClient({ apiURL, credential: () => session.currentTenantToken() }),
  appID, scope, definitionID, name: 'customer-export',
  receiptStore: createBrowserOperationReceiptStore(), // optional durable metadata
  onChange: update => renderExport(update),
});
await feature.history();
const restored = await feature.resume(); // lookup only; never submits work
// If unresolved, ask for the same input before an explicit retry.
submitButton.onclick = () => feature.start({ count: 100 });
// Close before signout or a customer switch; late responses cannot select work.
signOutButton.onclick = () => feature.close();
```

The session coalesces duplicate clicks, snapshots input, preserves its key across
uncertain in-session retries, resumes progress and fences late responses after
selection changes. `result()` refreshes confirmed business success independently
of delivery; `download(artifactID?)` retrieves an attached retained file. An
accepted submission remains accepted if a follow-up read fails. History is live
and deduplicates overlapping pages. The controller never persists credentials or
input. Without a receipt store, pending identity lives only in memory.

Opt into `createBrowserOperationReceiptStore()` to save submission metadata before
POST. It requires localStorage and Web Locks in a secure browser context and
fails before submission if unavailable. Storage contains verified API/account/customer,
app/environment/name, frozen definition, key, a local input fingerprint and an
acceptance acknowledgement; it contains no bearer token, raw input, results or
progress. Signout keeps this metadata for the same customer. Shared Web Locks
coordinate tabs, including concurrent starts and resumes.

`resume()` returns `empty`, `unresolved` or `accepted` and restores current
status/progress on acceptance. It never automatically submits work. For
`unresolved`, re-enter identical input and explicitly call `start`; the saved
key and immutable definition are reused across releases. Explicit browser submission
keys must be bounded ASCII and match their HTTP header exactly (no edge whitespace).
The fingerprint compares local retry input; server canonical idempotency remains authoritative.
Unconfirmed retries stop after one day. Known expired or missing acknowledged
acceptance also remains blocked for history inspection; do not clear saved
metadata as a network-error retry. A new explicit start can replace an accepted
receipt already resolved by that session. Close the session before changing
customers. Applications with equivalent transactional persistence can inject an
`OperationReceiptStore`; its exclusive lock must span awaited requests and its
`save` must finish durably before returning.

For applications that embed a customer Operations feature,
`CustomerOperationFeature` owns authentication, session setup, history loading,
receipt resumption and identity-change teardown. The host login adapter remains
responsible for returning the current tenant-bound token and reporting account
changes:

```ts
import { CustomerOperationFeature, createBrowserOperationReceiptStore } from '@gregale/sdk-node/operations';

type ExportInput = { count: number };
type ExportResult = { artifact_id: string; rows: number };

const feature = new CustomerOperationFeature<ExportInput, ExportResult>({
  apiURL, appID, scope, definitionID, name: 'customer-export',
  provider: {
    getCredential: () => appAuth.getCustomerOperationsToken(),
    onIdentityChange: callback => appAuth.onIdentityChange(callback),
  },
  receiptStore: createBrowserOperationReceiptStore(),
  onChange: update => render(update),
  onIdentityChange: () => clearOperationUI(),
});

const connection = await feature.connect();
if (connection) {
  renderRestoredReceipt(connection.restored);
  const input: ExportInput = { count: 100 };
  await connection.session.start(input);
}
// Disconnect keeps the host login listener active; dispose it with the page.
feature.close();
feature.dispose();
```

`connect()` preflights a fresh credential, creates the client and session, loads
history, then resumes any retained submission receipt. It returns `undefined`
if a newer connect or identity change supersedes it. `close()` invalidates the
active session but keeps listening for identity changes; `dispose()` also
unsubscribes. The separate `CustomerOperationAuth` helper remains available for
applications that need to compose credentials into a custom lifecycle. A
fallback credential callback supports local token-form demos. The input generic
types the explicit `start()` payload and the output generic types operation
snapshots; server-side JSON Schema validation remains authoritative at runtime.
Keep account keys and bearer tokens in the host auth system.

`GregaleOperationClient.lookupSubmission({app_id, scope, name, idempotency_key,
expected_identity?})` reads a customer-scoped retained receipt. It returns
`accepted` with original `accepted_at` and read routes, `expired`, or `unresolved`.
Unresolved is not proof of rejection. Optional `expected_identity` and
`expected_scope` on start fence principal and feature changes without granting
ownership. Lookup is available with read scope while admission is closed.
For an immutable definition with `http_transaction_version: 1`, explicitly
install `customerOperationReceiptSchema` in the application PostgreSQL database.
Call `operations.transaction({ headers, method, path, body }, pool, async tx => result)`
after business authorization, using the original request target and body bytes.
Send the returned `body` as JSON without re-encoding it. The business writes and
result receipt commit together; later authorized executions return the saved
bytes with `replayed: true` and skip the callback. The callback must use only the
supplied transaction and must not commit, roll back, or perform external effects.
Unknown COMMIT is surfaced as `OperationCommitUnknownError`; keep the original
Operation identity for recovery. This customer protocol returns the complete
business result and uses a separate receipt table from managed operations.
The [handler integration](../../docs/operations.md#postgresql-http-handler-transactions)
includes an example and retention requirements. The runnable
[order-fulfillment example](../../examples/customer-operation-orders/README.md)
adds source declarations, deployment packaging, explicit database setup, and
progress after commit. Definition discovery exposes the pinned transaction version.

Completion delivery inspection, attempt history, and immutable retry decisions
are exposed through the Operations APIs (`getOperationDelivery`,
`getOperationDeliveryAttempts`, `retryOperationDeliveryWithReceipt`; PascalCase
in Go and snake_case Python modules). New retries carry `retry_id`, `delivery_id`
and an explicit `expected_replay_generation`, including zero. Reuse the same
request after an uncertain reply; the returned `queued` receipt describes the
original decision. Read delivery status separately. Business results and
execution generations are unaffected. The legacy retry method remains available.


## Object version protection

The Storage API supports typed retention/legal-hold reads and mutations, plus
protection operation inspection. Use an explicit owned public version UUIDv4
(or `null` in an eligible Object Lock bucket). Mutations require a stable UUIDv4
operation ID and return a durable receipt; retain the returned ID for retries
and status. Fixed GOVERNANCE/COMPLIANCE retention and independent ON/OFF legal
holds are supported. Event-hold changes and governance bypass are unsupported.
See [the protection contract](../../docs/object-storage.md#per-version-retention-and-legal-holds)
for enrollment, pending-operation fences and recovery behavior.

Workflow-backed Operations use `GregaleWorkflowOperations` from
`@gregale/sdk-node/operations/runtime` and their own trusted request proof.
Use `runCancellableRequest(req.headers, async scope => ...)` in each action.
Pass `scope.signal` to I/O and checkpoint between work units. The scope reads
current native control before work and before accepting a successful result,
and stops at cancellation, lost authority or the fixed attempt deadline/lease.
Control reads never renew the native lease. In the final action, call
`runtime.uploadArtifact({report_id: 'export-csv', name: 'export.csv', data, maxBytes})`.
The helper snapshots and hashes bounded bytes, checks for a durable receipt before
every transfer retry and observes the scope's abort signal. It needs no bucket,
source URI or provider credential. Keep the report ID, name and bytes stable across
approved resumes: the same workflow run and final step retain the file identity,
while fresh native authority rebinds the receipt to the current attempt. Receipt
reuse does not consume another report or transfer the bytes again. Files remain
private until the final step succeeds or explicit success recovery supplies typed
output and evidence. Cancellation fences uploads and receipt binding; an already
verified private copy remains available for approved recovery. See the
[workflow export example](../../examples/customer-operation-workflow-export/README.md).
`runRequest` remains available for manual cooperative control.

For managed sources, `prepareArtifact({report_id, name, uri, data, maxBytes})`
and `uploadAndAttach(existingBucketWriter)` remain available. Keep the `obj://`
source key stable across resumes and observe the writer's optional `signal`.
An uncertain external write without a verified copy still requires provider
reconciliation; cancellation cannot prove that the external write was undone.

Recovery decisions: Generated `OperationsService.recoverOperationWithReceipt` returns `OperationRecoveryDecision`. It acknowledges the original explicit
account-authorized decision, independently of current operation and delivery
status. Retrying the same decision ID and request never records a second
recovery. See [receipt-backed operator recovery](../../docs/ops/customer-operations-cli.md#resume-a-recovery-decision-after-losing-its-response).

Native batch Job Operations use `runJobOperation({ apiURL }, async (input, scope) => result)` from `@gregale/sdk-node/job-operations-runtime`. The scheduler supplies a task capability and customer identity. Use `scope.operation.platformTenantID` for business ownership, `scope.progress(...)` to report stages, and `scope.signal`/`scope.checkpoint()` during work. The helper never retries business code. It prepares a typed result receipt; the host confirms success on task exit. Production admission remains closed pending native qualification.

Initialize the complete Job export feature with `gregale init --template
customer-operation-job-export --path exports`. Its
[README](../../cmd/gregale/templates/customer-operation-job-export/README.md)
connects the installed SDK, Job image, direct private file uploads, typed result
and browser session. The Job uses its scheduler capability for uploads.

```ts
const result = await runJobOperation({ apiURL }, async (input, scope) => {
  const file = await scope.uploadArtifact({
    report_id: 'customer-export-csv', name: 'export.csv',
    data: csvBytes, maxBytes: 1024 * 1024,
  });
  return { file: file.id };
});
```

`uploadArtifact` accepts text or `Uint8Array` and snapshots the bytes before I/O.
Its default application memory bound is 8 MiB; `maxBytes` can override it. The
API independently enforces the operation's captured plan quotas. A stable report
ID binds name, size and SHA-256; changed declarations conflict. Matching calls
share a receipt. Before retrying an interrupted platform transfer, the SDK checks
for a durable receipt, including after a lost acknowledgement. The handler runs
once. An opaque `operation://.../artifacts/...` reference contains no physical
storage key; use the customer's scoped download API.

Files stay private until the host confirms successful exit with typed output,
or account-authorized success recovery supplies valid output and evidence.
An uncertain native outcome requires reconciliation. An approved retry uses a
fresh Job run and fresh file receipts. Escaped scopes stop after handler exit.
Completion delivery remains independent of the business result.

`scope.prepareArtifact({report_id, name, uri, data, maxBytes})` and
`uploadAndAttach(writer)` remain available for existing private managed sources.
They check for a retained receipt before calling the bucket writer and do not
repeat an uncertain external write. `attach()` can reconcile an existing source.
Production admission remains closed pending native qualification.
Customer HTTP transactions can declare business milestone schemas in their source manifest. Install the current `customerOperationReceiptSchema`, then call `tx.milestone('order-fulfilled', {order_id, status: 'fulfilled'})` inside `GregaleOperations.transaction`. The SDK validates before commit and saves a durable outbox with the business write and result receipt. It publishes after commit; `OperationMilestonePublicationError.committed` identifies a pending publication that recovery of the same Operation can replay without repeating business work.

To report the current state of a workflow instance, declare its accepted values under `operation_workflows[].states` and call `tx.workflowState('order-lifecycle', workflowRunID, 'completed')` in that same transaction. Optionally list terminal values under `operation_workflows[].terminal_states`; each must be a declared state and cannot have an outgoing transition. The workflow read API returns `terminal: true` for a reported state in that list and `false` otherwise. If the workflow declares `transitions`, call `tx.workflowTransition('order-lifecycle', workflowRunID, 'fulfillment-in-progress', 'completed')`; check the source value against the locked business row first. The SDK validates that the edge is declared and, when a prior state report exists, checks that `from_state` matches it before commit. A mismatch aborts the business transaction. The first report can establish history, so the application still checks the business row. The SDK allocates an increasing revision per workflow instance and stores the report in the app-side outbox. It publishes after commit, and recovery retries pending reports. A committed publication failure is reported as `OperationWorkflowStatePublicationError` with `committed = true`. Business-reference reads expose the newest revision and update time; a delayed older report cannot replace it. States are explicit application reports.

Generate typed workflow states, constants, and transition helpers with
`gregale customer-operations bindings --app orders --plan pro --language typescript --output workflow-bindings.ts`.
JavaScript applications use `--language javascript` and an `.mjs` output.
The generated helper takes the transaction, instance ID, locked source state,
and required milestone payloads; it queues both facts and the transition.
Run the same command with `--check` in CI. See the
[binding guide](../../docs/operations.md#generate-application-workflow-bindings).

Customer clients read `client.milestones(operationID, {limit, cursor})` and `client.businessMilestones({appID, scope, subjectType: 'order', subjectID: orderID})`. Add `workflow` and `workflowInstanceID` together to select one workflow run. That response includes its retained `workflow_state_history` and a grouped `workflow_instance` view with ordered steps, contract-declared `allowed_transitions` by target Operation, the current explicit state, and page-scoped plus retention-wide fact summaries per step. Required milestones on an allowed edge must be committed with the transition; the application still checks its business row and authorization. Retention summaries cover the selected contract version. A step with no retained fact may have expired evidence, so its absence does not prove it never occurred. Continue with `next_cursor` and `next_workflow_state_cursor`, or use the snapshot aliases `next_milestone_cursor` and `next_transition_cursor`; `has_more` is true while either page has more data. Business references preserve the existing customer boundary, and each cursor is bound to all filters. Milestone payloads must contain only schema-declared public JSON facts. See [the Operations guide](../../docs/operations.md) for limits and recovery semantics.

Business-reference responses also include current workflow states where the application has reported one. Each entry has a workflow name, instance ID, state, terminal and stale classifications, the app-reported occurrence time, revision, and publication update time. Set `staleOnly: true` on `businessMilestones` to filter its current-state entries to runs beyond their app-declared `state_stale_after` threshold; milestone facts and state history remain unchanged.

### Typing indicators and temporary client signals

Named signals replace the sender's earlier value under the same name and
expire automatically in `createRealtimeSignalTracker`. Use them for typing,
cursors, or a short-lived "viewing" indicator:

```ts
import WebSocket from 'ws';
import {
  consumeRealtimeChannel, createRealtimeSignalTracker,
  type RealtimeChannelActions,
} from '@gregale/sdk-node';

const tracker = createRealtimeSignalTracker((active) => renderRoomActivity(active));
let room: RealtimeChannelActions | undefined;
const stop = new AbortController();
const consumer = consumeRealtimeChannel({
  url: 'wss://app.example.com/__gregale/realtime/ENDPOINT_ID',
  channel: 'room-a',
  cursorStore: roomCursorStore,
  signal: stop.signal,
  onMessage: async (message) => applyRoomEvent(message),
  onSubscribed: (actions) => { tracker.clear(); room = actions; },
  onSignal: (signal) => { tracker.accept(signal); },
  onPresence: (event) => {
    if (event.event === 'left' && event.memberId) tracker.removeMember(event.channel, event.memberId);
  },
  webSocketFactory: async (url, protocols) => new WebSocket(url, protocols, {
    headers: { Authorization: `Bearer ${await getFreshOidcToken()}` },
  }),
});

// Call from UI events, throttled below the channel signal limits.
function onTyping() { room?.sendTemporarySignal('typing', true, 5000); }
function onTypingStopped() { room?.clearTemporarySignal('typing'); }
function onCursorMove(x: number, y: number) { room?.sendTemporarySignal('cursor', { x, y }, 1000); }

try { await consumer; } finally { tracker.dispose(); }
```

Browser applications use the same exports from `@gregale/sdk-node/browser`
with `createBrowserRealtimeSocketFactory`. Temporary signals require upgraded
apid and realtime nodes plus the existing resume preview flag. No migration is
required. Channels still need a cursor store or durable subscription because
signals share the established v2 channel subscription.

`sendTemporarySignal(name, data, ttlMs)` defaults to 5000 ms, permits 0..30000
ms, and accepts names of 1..64 ASCII letters, digits, underscores, hyphens,
dots, or colons. Zero is an explicit clear. Payloads remain JSON bounded to
2 KiB. Signals use the existing subscription permissions, 20-per-second
connection limit, and 50-per-second channel limit on each sending node.
They go to other currently connected v2 subscribers and are never retained,
acknowledged, or replayed. A successful send does not guarantee delivery;
`onEphemeralRejected` reports rate limits and relay failures.

The node supplies `updatedAt` and `expiresAt` through `onSignal`, along with
`name`, `channel`, `memberId`, and `data`. The tracker keys state by
channel/member/name, ignores unnamed signals, drops older timestamps, and
uses timers to notify `onChange` when a value expires without another incoming
message. A clear removes state immediately. It retains brief tombstones to
avoid undoing a clear with delayed older updates and caps tracked entries,
including tombstones, at 256; excess entries evict the least recently applied.
Expiry compares the local clock to server timestamps and is bounded to 30
seconds after receipt even with clock skew. Keep client clocks reasonably in
sync for accurate expiry, and dispose the tracker when the view closes.

Clear the tracker on each subscription acknowledgement: temporary state has
no reconnect snapshot. TTL handles disconnects or lost clear events; presence
leave events can clear a member sooner. Default connection presence gives
each device a separate member ID. With principal-grouped presence, devices
share a member ID and can replace each other's named values. Applications
should refresh active indicators before expiry and throttle cursor movement.

### Applying retained message edits and deletions

Channel and inbox `onMessage` callbacks now include `version`, `event`,
`deleted`, and optional `targetMessageId`. Initial versions are 1. Mutations
arrive as separate ordered messages and are ACKed only after your callback
resolves, just like initial messages. Use the stable target ID for application
records and the stream sequence for delivery progress:

```ts
async function applyVersionedMessage(message) {
  const id = message.targetMessageId ?? message.messageId;
  // Namespace the record by endpoint and channel, or by endpoint and principal.
  await database.transaction(async (tx) => {
    const current = await tx.lockMessage(roomKey, id);
    if ((current?.version ?? 0) >= message.version) return;
    if (message.deleted) {
      // Keep the version even after removing content to prevent old replays.
      await tx.redactMessage(roomKey, id, message.version);
    } else {
      await tx.upsertMessage(roomKey, id, message.version, message.data, message.binary);
    }
  });
}
```

Use this as the channel or inbox `onMessage` handler. Earlier retained entries
can carry a newer snapshot after an edit, and multiple entries may carry the
same version, so the version guard is necessary even with ordered delivery.
Channel `messageId` identifies the delivery sequence; `targetMessageId` is its
publisher idempotency key. Inbox `messageId` is already the stable notification
ID. A tombstone has `deleted: true` and empty payload bytes. The SDK still
advances the cursor after a skipped duplicate-version projection.

Backend edits and deletions use the API or `gregale realtime edit-message` /
`delete-message`, with `--expected-version` and a stable publisher ID. See the
[operations guide](../../docs/ops/realtime.md#editing-and-deleting-retained-messages).
Upgrade apid, realtime nodes, the SDK, and application handlers after applying
the mutation migration. Existing handlers that interpret every message as a
new payload must be updated before using edits or deletion.

### Read receipts

Read receipts are explicit user actions. Processing a websocket message or
saving its cursor does not mark it read. Channel actions now provide
`markRead(sequence)` and `refreshReadProgress()`, with results delivered to
`onReadProgress` and failures to `onReadError`.

For an inbox, use `onReadActions` to obtain the same controls:

```ts
import WebSocket from 'ws';
import { consumeRealtimeInbox, type RealtimeReadActions } from '@gregale/sdk-node';

let reads: RealtimeReadActions | undefined;
let processedThrough = 0;
const consumer = consumeRealtimeInbox({
  url: 'wss://app.example.com/__gregale/realtime/ENDPOINT_ID',
  inbox: {
    consumerId: persistedDeviceId,
    onMessage: async (message) => {
      await saveNotificationVersion(message);
      processedThrough = message.sequence;
      reads?.refreshReadProgress();
    },
    onReadActions: (actions) => { reads = actions; },
    onReadProgress: (progress) => {
      renderUnreadBadge(progress.unread, progress.historyUnavailable);
    },
    onReadError: (error) => showReadSyncStatus(error.code),
  },
  webSocketFactory: async (url, protocols) => new WebSocket(url, protocols, {
    headers: { Authorization: `Bearer ${await getFreshOidcToken()}` },
  }),
});

// Call from a user interaction after message processing has completed.
function onNotificationsOpened() { reads?.markRead(processedThrough); }
await consumer;
```

Throttle refresh calls for batches: the connection limit is 20 read requests
per second. Browser apps use these exports from `@gregale/sdk-node/browser`.
Channel consumers obtain read actions through their existing `onSubscribed`
callback and supply `onReadProgress` / `onReadError` on the channel options.

Progress is shared across the principal's devices and never moves backward.
`RealtimeReadProgress` includes `readerId`, `inbox`, optional `channel`,
`sequence`, `unread`, retained bounds, and `historyUnavailable`. Unread counts
are distinct retained, undeleted messages after that watermark; an edit can
make a message unread again. A retention gap means older unseen messages are
missing from the count. The SDK reads the current state after each successful
subscription when a progress callback is present, but it never marks a message
read automatically. Channel receipts can describe other readers; distinguish
reader IDs before updating a personal unread badge.

`markRead` accepts only a nonnegative safe integer up to the last fully
processed sequence for that subscription. It sends a request; confirmation is
the progress callback, rather than the method returning. A disconnected action
throws, and an unconfirmed request may need resending after reconnect.
Storage is monotonic, so duplicate marks are safe. A persisted write can
succeed even if fleet notification later fails; refresh state to reconcile.
Reading does not change delivery ACKs or cancel notification fallback timers.

Apply the read-progress migration and upgrade apid and realtime nodes before
using these controls. Live events require the read opt-in that the SDK adds
when callbacks are supplied; existing clients are not sent unknown read frames.
[API, CLI, and webhook details](../../docs/ops/realtime.md#read-receipts-and-unread-counts)
are in the operations guide.

### Push notification registrations

Configure FCM, APNs, or Web Push on the endpoint with the account API/CLI. A
verified inbox consumer can then register a token over its current socket:

```ts
import { realtimeWebPushRegistration } from '@gregale/sdk-node/browser';

await consumeRealtimeInbox({
  // ...connection options...
  inbox: {
    consumerId: 'browser-main',
    onMessage: async message => { await persist(message); },
    onPushActions: actions => {
      actions.registerPush(realtimeWebPushRegistration(subscription.toJSON()));
      // Mobile: actions.registerPush({ provider: 'fcm', target: { token } });
      // actions.unregisterPush() removes this consumer's registration.
    },
    onPushRegistered: registered => { /* registration confirmed */ },
    onPushError: code => { /* handle an asynchronous registration error */ },
  },
});
```

Your app obtains permission and creates the browser PushSubscription using the
configured VAPID public key. It also owns the service worker and notification
click behavior. The SDK helper converts `PushSubscription.toJSON()` into the
registration wire shape. Repeated registration with the same token preserves
pending deliveries; changed tokens cancel work for the previous registration.
Push actions belong to the current connection and are supplied again on reconnect.

Push fallback requires retained inbox sends with `fallback_after_seconds`. Push
payloads contain notification text and inbox identifiers, without message bodies.
Fetch the inbox after a notification opens the app. Deduplicate by `delivery_id`:
provider acceptance can be retried after a worker crash. ACKs cancel queued push,
while read markers remain independent. See `docs/ops/realtime.md` for provider
configuration, retry limits, and the delivery history API.

### Notification preferences and quiet hours

The `RealtimePushActions` supplied to an authenticated inbox consumer also expose
`setNotificationPreferences()` and `refreshNotificationPreferences()`. Settings
apply to the current verified user across registered devices on this endpoint.

```ts
onPushActions: actions => {
  actions.setNotificationPreferences({
    enabled: true,
    categories: { chat: true, jobs: false },
    devices: null,
    quiet_hours: { timezone: 'Europe/Rome', start: '22:00', end: '07:00' },
  });
},
onNotificationPreferences: preferences => { /* render settings */ },
onNotificationPreferencesError: code => { /* report a rejected request */ },
```

`enabled` is required; the update replaces all prior settings. Unlisted
categories remain enabled. Omit or set `devices` to `null` to select all devices;
use `[]` to select none, or a list of inbox consumer IDs to choose devices.
Omit or set `quiet_hours` to `null` to disable quiet hours. Times are daily
`HH:MM` values in a named timezone; overnight intervals and daylight-saving
changes are supported. Start and end must differ.

A preference callback fetches current settings after every reconnect. Changes
return through the same callback. Socket actions are bound to the current
connection; supply them again after reconnect. The backend sets the notification
category on retained sends using `notification_category` with a fallback deadline;
the default category is `notifications`. Quiet-hour alerts remain pending until
the window ends, and an inbox ACK cancels them in the meantime. Preferences
control built-in push without affecting inbox retention or message consumption.

### Grouped notification digests

Notification preferences also accept `digest_interval_seconds` (0, 300, or 3600)
and `summarize_quiet_hours` (default true). Use the existing authenticated inbox
push actions to read or replace the complete preference document:

```ts
onPushActions: actions => {
  actions.setNotificationPreferences({
    enabled: true,
    devices: null,
    categories: { chat: true },
    digest_interval_seconds: 300,
    summarize_quiet_hours: true,
    quiet_hours: { timezone: 'Europe/Rome', start: '22:00', end: '07:00' },
  });
},
onNotificationPreferences: settings => { /* update the digest selector */ },
```

Backend retained sends can provide `notification_group_key` and
`notification_group_label` along with category and fallback deadline, for example
`project-alpha` / `Project Alpha`. The backend groups eligible events per user,
device, category and key into fixed UTC delivery windows. Quiet-hour release can
produce a summary even for immediate delivery; set `summarize_quiet_hours: false`
to keep individual alerts when the digest interval is zero.

The push payload includes `message_count`, `group_key`, and a stable
`delivery_id` for the digest. Use that ID for deduplication and fetch the inbox
on open. Web Push service workers should pass the provided `tag` into
`showNotification()` to replace provider retries. Every individual inbox event
keeps its own sequence and acknowledgement behavior; acknowledged events are
excluded from a summary prepared afterward. Digests have at most 128 members,
and later arrivals can produce another summary for the same group/window.

### Urgent notification preference

Users can explicitly allow urgent alerts to bypass quiet hours and digests:

```ts
onPushActions: actions => {
  actions.setNotificationPreferences({
    enabled: true,
    allow_urgent_bypass: true,
    digest_interval_seconds: 300,
    quiet_hours: { timezone: 'Europe/Rome', start: '22:00', end: '07:00' },
  });
},
```

These actions replace the complete preference document; include any category and
device restrictions you want to keep. The default `allow_urgent_bypass` is false.
Your backend marks retained fallback sends with `notification_priority: 'urgent'`
(or `low` / `normal`). Urgent alerts still respect mute settings and the fallback
acknowledgement deadline. Inbox ACKs cancel queued alerts. Priority appears in
provider payloads and delivery history, and digest groups never mix priorities.

### Push rate limits

The authenticated notification preference actions support a shared quota across
a principal's devices within one endpoint:

```ts
onPushActions: actions => {
  actions.setNotificationPreferences({
    enabled: true,
    rate_limit: {
      max_notifications: 5,
      window_seconds: 60,
      allow_urgent_bypass: false,
    },
  });
},
```

`max_notifications` accepts 1–100 and `window_seconds` accepts 60, 300 or 3600.
Omit `rate_limit` or set it to null to disable it. Preference writes replace the
whole document, so preserve any other settings you need. Each digest per device
counts as one slot in a fixed UTC window. Compatible alerts can form summaries;
overflow waits until the next window, still respecting expiration, ACKs and
collapse keys. The nested urgent bypass is independent of the top-level bypass
for quiet hours and digests. History exposes `rate_limited` and `next_attempt`.

### Scheduled backend notifications

Your backend can set `notification_not_before` on retained fallback sends, for
example `"2026-10-09T09:00:00Z"`. It requires a fallback acknowledgement deadline
and accepts RFC3339 timestamps up to 48 hours ahead. Inbox publication is
immediate; built-in push waits for both the schedule and acknowledgement deadline.
Clients can acknowledge the inbox message to cancel the scheduled alert.
Quiet hours, digests and rate limits still apply afterward. Urgent opt-in cannot
bypass this explicit schedule. If you set a notification TTL, it must extend past
the schedule and still starts at publication. Delivery history exposes
`not_before`, `scheduled` and `next_attempt`.

### Recover from a channel snapshot

Load snapshots through an application-authorized backend route, then commit state
before returning the replay baseline:

```ts
import { recoverRealtimeChannelSnapshot } from '@gregale/sdk-node';

const recover = () => recoverRealtimeChannelSnapshot(
  'jobs',
  async () => {
    const response = await fetch('/api/jobs/snapshot');
    if (!response.ok) throw new Error(`Snapshot unavailable: ${response.status}`);
    return response.json();
  },
  snapshot => commitJobState(snapshot.data),
);
// Pass await recover() as initialSequence, or use onResync: recover.
```

Use your package's normal import path. The helper validates channel, sequence,
expiration and the 64 KiB limit, decodes bytes, awaits your state commit, and
returns `resume_after_sequence`. Snapshot expiration or a missing replay tail
requires a fresh snapshot. Keep management credentials on the backend.

### Atomic backend batches

`publishRealtimeChannelBatch` prepares and validates a batch, calls your authorized
transport, and validates the returned contiguous sequences:

```ts
import { publishRealtimeChannelBatch } from '@gregale/sdk-node';

const encode = (value: unknown) => new TextEncoder().encode(JSON.stringify(value));
const result = await publishRealtimeChannelBatch('job-123-completed', [
  { data: encode({ progress: 100 }) },
  { data: encode({ status: 'completed' }) },
], async request => {
  const response = await fetch('/api/jobs/publish-batch', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(request),
  });
  if (!response.ok) throw new Error(`Batch failed: ${response.status}`);
  return response.json();
});
```

Your backend submits the request to the channel's `publish-batch` management API.
Reuse the same ID and content on retries. A batch supports 1–32 messages, 4 KiB
each and 64 KiB total. A partial fanout result still means the batch is durably
committed. Durable atomicity does not imply simultaneous UI delivery.

### Filter retained channel events

Set `filter: { project_id: 'alpha' }` on a channel consumer. Publish events with
matching `metadata` through retained channel publishing or atomic batches.
Filters use exact matches joined with AND. Missing metadata does not match.
Received messages expose `metadata`; skipped events advance and acknowledge the
cursor through checkpoint frames without invoking `onMessage`.

Use distinct cursor stores or durable subscription names for different filters.
Changing a filter on an existing cursor does not recover skipped historical
messages. Channel authorization still controls access, and filters do not affect
presence, signals or direct messages. Batch helper items accept `metadata` too.

### Versioned event contracts

Register immutable JSON Schema Draft 2020-12 versions through your backend's
management API, then select them in publisher metadata:

```ts
import { realtimeEventSchemaMetadata } from '@gregale/sdk-node';

const metadata = realtimeEventSchemaMetadata('job.progress', 1, { project_id: 'alpha' });
// Supply this metadata on a retained publish or a batch item:
const item = {
  data: new TextEncoder().encode(JSON.stringify({ job_id: 'job-123', progress: 75 })),
  metadata,
};
```

The first registered schema enables enforcement for new retained events in its
channel. Every event must then identify a registered type/version. Invalid events
return field errors; one invalid batch item rejects the entire batch. Versions
remain immutable, so register a new version when changing the contract.

### Built-in channel reducers

Enable a reducer through your backend management API using a seed entity map and
its exact current channel sequence. Retained operation events then maintain state
and channel snapshots atomically:

```ts
import { realtimeReducerEvent } from '@gregale/sdk-node';

const data = realtimeReducerEvent({
  op: 'merge',
  key: 'job-123',
  value: { progress: 75 },
});
// Publish data as a retained event, or pass { data } as a batch item.
```

`set` replaces an entity object; `merge` overwrites top-level fields; `delete`
removes the entity. Limits are 128 entities and 64 KiB of state, with 4 KiB per
event. Schema-enabled channels still require event schema metadata. Use the
existing snapshot recovery helper to read reducer-generated snapshots through
your authorized backend. Active reducers prohibit edits to historical events and
manual snapshot writes; publish compensating operations instead.

### Conditional channel publishing

Use a channel head or reducer snapshot sequence to avoid overwriting newer state:

```ts
import { realtimeExpectedSequence } from '@gregale/sdk-node';

const body = {
  ...realtimeExpectedSequence(42),
  data_base64: '...',
};
// Submit as a retained channel publish with a stable idempotency key.
```

For `publishRealtimeChannelBatch`, pass `{ expectedSequence: 42 }` as its fourth
argument. Omission is unconditional; zero requires an empty channel. A stale
precondition returns 409 with code `realtime_sequence_conflict` and the problem's
`expected_sequence` / `current_sequence`. Reload and reconcile state before
retrying. Matching idempotent retries return the original publication even if the
head has advanced. The check applies to the whole channel, across all entities.

Reducer operations support per-entity concurrency checks. Read `entity_versions`
from reducer state or a reducer snapshot, then include the expected version:

```ts
const data = realtimeReducerEvent({
  op: 'merge', key: 'job-123', value: { progress: 75 }, expected_version: 4,
});
```

Use version 0 for a key that has never existed, or omit the field for an
unconditional operation. Deleted keys retain their versions. A conflict returns
409 `realtime_entity_version_conflict` with the key, current version, existence,
and batch item index. Batch checks are atomic and observe earlier batch items.

Temporary reducer entities can expire automatically:

```ts
const typingEvent = realtimeReducerEvent({
  op: 'set', key: 'typing:user-123', value: { active: true },
  expires_at: new Date(Date.now() + 10_000).toISOString(),
});
```

Deadlines must be in the future and within 30 days according to the server.
Set without a deadline clears it; merge without one preserves it. Supply
`expires_at: null` on set/merge to clear a deadline. Reducer state and recovered
snapshots expose `entity_expirations`. Cleanup runs asynchronously in bounded
five-second passes and emits retained delete events with `reason: "expired"`
and metadata `event_type: reducer.expired`; include that event type in filters.
Entity version checks prevent old timers from deleting refreshed state.

Atomic counters use the same retained publish or batch transport:

```ts
const data = realtimeReducerEvent({
  op: 'increment', key: 'post-123', field: 'likes', delta: 1,
  min: 0, max: 1_000_000,
});
```

Missing fields start at zero. Negative deltas decrement; bounds reject updates
instead of clamping. Existing values, deltas, bounds, and results must be safe
integers. Optional `expected_version` checks and atomic batches work normally.
Increment preserves an entity deadline unless `expires_at` replaces or clears it.

Atomic arrays use the same reducer event helper:

```ts
const join = realtimeReducerEvent({
  op: 'append', key: 'room-123', field: 'members',
  items: ['user-1', 'user-2'], unique: true, max_length: 100,
});
const leave = realtimeReducerEvent({
  op: 'remove', key: 'room-123', field: 'members', items: ['user-1'],
});
```

Missing fields start as empty arrays. Append optionally skips structurally equal
items; remove deletes all matches. Objects compare independently of key order,
arrays compare in order, and numbers compare by exact numeric value. Supply
1–32 JSON items; arrays are limited to 256 items, or a smaller `max_length` for
that operation. Invalid values and length violations reject the whole batch.
Entity version checks, idempotent retries, and optional deadlines work normally.

Use atomic field conditions to claim a queued job safely:

```ts
const claim = realtimeReducerEvent({
  op: 'merge', key: 'job-123', value: { status: 'running', worker: 'worker-1' },
  conditions: [
    { field: 'status', equals: 'queued' },
    { field: 'worker', absent: true },
  ],
});
```

All 1–16 predicates must pass. Conditions support structural JSON equality or
field absence; `equals: null` differs from absence. They work on every reducer
operation and observe earlier batch updates. A failure rolls back the entire
batch and returns 409 `realtime_condition_conflict`, with entity, field,
condition/batch indices, current version, and existence flags.
`RealtimeReducerCondition` is exported for both Node and browser clients.

Prepare a scheduled retained event for your authorized backend transport:

```ts
const request = realtimeScheduledEvent(
  new TextEncoder().encode(JSON.stringify({ kind: 'reminder' })),
  new Date(Date.now() + 60_000).toISOString(),
);
// PUT the request to .../channels/{channel}/schedules/{schedule_id}.
// Reuse the same schedule ID and request when retrying creation.
```

The server accepts future deadlines within 30 days and payloads up to 4 KiB.
List schedules with GET `.../channels/{channel}/schedules`; PATCH a schedule with
`deliver_at` and `expected_version`, or DELETE it with `?expected_version=...`.
Only pending schedules can change; cancellation retries are idempotent.
Schedules persist in PostgreSQL, publish to retained history atomically, and
then attempt live fanout. Delivery failures follow the configured retry policy
and expose `last_error`; exhausted schedules become `failed`. Cleanup
runs in bounded five-second passes. Terminal receipts remain visible for
24 hours; there are at most 256 pending/recent terminal schedules per endpoint.

Enable bounded automatic retries when creating a schedule:

```ts
const request = realtimeScheduledEvent(data, deliverAt, {
  maxAttempts: 5, backoffSeconds: 30,
});
const retry = realtimeScheduleRetry(failedSchedule.version);
// POST retry to .../schedules/{schedule_id}/retry via your authorized backend.
```

`maxAttempts` includes the initial attempt (1–10, default 1); backoff doubles
from `backoffSeconds` (5–3600, default 5), capped at one hour. Responses expose
lifetime `attempts`, `cycle_attempts`, latest failure, and next/last attempt times.
A manual retry starts a fresh cycle for a failed schedule with its existing ID
and payload; optionally pass a future time as the second helper argument.
Cancel with the latest schedule version to stop pending retries. Completed
publication is atomic and is never retried because live fanout failed.

Inspect a schedule with GET
`.../channels/{channel}/schedules/{schedule_id}/history?after_version=0&limit=50`.
The response implements `RealtimeScheduleHistory` in Node/browser exports.
Events include creation, rescheduling, cancellation, manual retries, recorded
failures with retry times, and publication with its committed channel sequence.

History keeps the latest 128 entries and expires with terminal schedule receipts.
Page using the last event's version and `has_more`; check `oldest_version` and
`history_truncated` for unavailable earlier entries. Migrated schedules start
with an incomplete `baseline`, rather than reconstructed historical attempts.

Repeat a retained event after each successful occurrence:

```ts
const recurring = realtimeScheduledEvent(data, firstDeliverAt, {
  intervalSeconds: 60, maxOccurrences: 100,
  maxAttempts: 3, backoffSeconds: 5,
});
```

Intervals use fixed delay after success, so outages/retries shift the series
without catch-up bursts. Each occurrence resets its retry budget; exhausted
failures stop the series for manual retry. Optional `endAt` limits new planned
occurrences. Recurring events carry `schedule_id` and `schedule_occurrence`
metadata, leaving room for six supplied metadata fields. State/history expose
occurrence numbers and successful completion counts.

POST `.../schedules/{schedule_id}/pause` or `/resume` with `expected_version`;
cancel a pending or paused series with the existing DELETE API. Pausing preserves
the deadline/budget, and resuming an overdue series makes its current occurrence
due. Paused records remain until resumed or canceled. `sequence` refers to the
latest successful publication, including when the next occurrence is pending.

Condition scheduled events on their channel's reducer state:

```ts
const conditional = realtimeScheduledEvent(data, deliverAt, {
  conditions: [
    { key: 'job-123', exists: true },
    { key: 'job-123', field: 'status', equals: 'pending' },
  ],
  onConditionFailure: 'skip',
});
```

Predicates are ANDed (1–16, at most 4 KiB) and support entity existence, structural
field equality, and safe-integer `lt`/`lte`/`gt`/`gte` comparisons. Checks use the
same channel's active reducer and run atomically with publication. Passing events
still undergo normal schema/reducer validation. `retry` is the default and uses
the attempt budget; `skip` records the reason without a channel event, advancing
recurring series if limits permit. Skips count toward the occurrence limit.
State/history expose `skipped_occurrences` and `skip_reason`.

Schedule completion app webhooks support event filters
`realtime.schedule.published`, `realtime.schedule.failed`, and
`realtime.schedule.skipped`. The exported
`RealtimeScheduleCompletionWebhookPayload` type describes occurrence identity,
outcome, counts, attempt information, and optional channel sequence/failure reason.
Each recurring publication or skip emits independently; failures emit only after
automatic retries are exhausted.

Use `verifyWebhook(secret, headers, rawBody)` before parsing the JSON envelope's
`payload` (or CloudEvents `data`). Store its returned `deliveryId` with receiver
side effects to deduplicate retries. The payload's `event_id` identifies one
outcome across webhook subscriptions. Existing webhook delivery/attempt APIs
provide inspection, and the delivery retry API retries dead deliveries.

Group schedules with `realtimeScheduledEvent(data, deliverAt, { group: 'campaign-123' })`.
List `/schedules?group=campaign-123` through your authorized backend; optionally
filter by `status`. `RealtimeScheduleList` includes status and occurrence totals
for the retained, filtered records.

```ts
const body = realtimeScheduleGroupRequest('campaign-123', list.schedules);
// Backend POST .../schedules/groups/campaign-123/pause (or resume/cancel), with body.
```

Build the body from a fresh group list without a status filter: it must include
all pending and paused members' versions. The server commits the whole group
action atomically or returns 409 if membership/versions changed. Relist before
retrying a conflict. Both one-time and recurring schedules support group controls;
terminal members are excluded. Totals are subject to the 24-hour terminal receipt
retention window.

Backends can publish transient signals using the channel `/signals` POST API.
Build the request with the Node/browser helper, then send it through an authorized
backend:

```ts
const request = realtimeBackendSignal({ active: true }, { name: 'typing', ttlMs: 5000 });
// Backend POST .../channels/{channel}/signals with request.
// Existing channel onSignal receives { memberId: 'backend', name, data, expiresAt, ... }.
```

Omit options for an unnamed signal. Use `realtimeBackendSignal(null,
{ name: 'typing', ttlMs: 0 })` to clear temporary activity. Data is limited to
2 KiB encoded JSON, and named signals expire within 30 seconds. Backend senders
share the `backend` identity; use distinct names for independent activities.
Signals are best effort, do not advance cursors, and are never replayed after
reconnect. The API's 20-per-second endpoint/channel limit is per API process;
429 responses include `Retry-After: 1`. A successful response does not acknowledge
subscriber receipt.

Throttle cursor/activity updates while keeping a final trailing value:

```ts
const cursor = createRealtimeSignalCoalescer(actions, 'cursor', {
  intervalMs: 100,
  ttlMs: 5000,
  onError: error => console.error('Cursor signal failed', error),
});
cursor.send({ x: 10, y: 20 });
cursor.send({ x: 15, y: 25 }); // replaces the pending value
cursor.flush(); // send the final position immediately, e.g. on pointer release
cursor.dispose(); // flush and stop; dispose(false) discards on disconnect
```

The helper snapshots JSON, sends a leading update, and schedules the latest
trailing value. Timer errors reach `onError`; the value remains pending for
explicit flush/retry. Direct send/flush errors throw. `cancel()` discards pending
data without sending a clear frame. Use channel `clearTemporarySignal` separately
when clearing receiver state. Server queues also coalesce named signals per
channel/sender/name and discard expired pending values before socket delivery.
Unnamed signals keep their existing behavior; publisher rate limits still apply.

Track typing indicators, cursors, and other named activity without managing expiry
timers:

```ts
const activity = createRealtimeActivityTracker({
  onChange: entries => renderRemoteActivity(entries),
});
// In channel options: onSignal: signal => { activity.apply(signal); }
const current = activity.snapshot();
activity.reset('room-123'); // clear this channel on reconnect/unsubscribe
activity.dispose(); // stop timers and clear activity on teardown
```

Entries are keyed by channel, sender member ID, and name. Newer updates replace
activity; explicit clears and expired newer updates remove it. Unnamed signals,
duplicates, and older timestamps are ignored. Snapshots and callback data are
isolated JSON copies, so UI mutations cannot change tracker state. Each channel
can share the tracker through its existing `onSignal` callback.

The tracker uses server expiry timestamps and the client's clock. Updates more
than 30 seconds in the future or past are ignored; keep clocks reasonably aligned.
Sub-millisecond timestamps preserve ordering. Short-lived clear/expiry markers
prevent older updates from restoring activity. `maxEntries` defaults to 1024
(including those markers), accepts 1–8192, and throws on capacity without evicting
active entries. Call `reset(channel)` when a live channel reconnects or leaves;
signals have no reconnect replay. Reset also clears that channel's ordering memory.
`dispose()` notifies with an empty snapshot when activity existed and rejects
future updates. Callback exceptions propagate; expiry callbacks run from a timer.

For automatic channel lifecycle management, set `onActivityChange` directly on
`consumeRealtimeChannel` options or a channel in `consumeRealtimeConnection`:

```ts
await consumeRealtimeChannel({
  url,
  channel: 'room-123',
  cursorStore,
  webSocketFactory,
  signal: abortController.signal,
  onMessage: message => handleMessage(message),
  onActivityChange: entries => renderRemoteActivity(entries),
  activityMaxEntries: 1024, // optional; includes ordering markers
});
```

The consumer owns one tracker per opted-in channel and feeds it named signals.
Disconnect, abort, resync, and unsubscribe acknowledgements clear that channel's
visible activity and ordering memory. Presence `left` removes the departed
sender's existing activity immediately, keeping short-lived ordering markers.
Raw `onSignal` and `onPresence` still run after the tracker update. Backend activity
has no presence membership and clears through expiry or explicit signals.

Activity callbacks are synchronous, including timer expiry notifications; handle
UI errors within the callback. `activityMaxEntries` requires `onActivityChange`
and accepts 1–8192. Activity is empty after reconnect and is rebuilt from new live
signals. Standalone trackers also expose `removeMember(channel, memberId)`.

Build UI-ready activity summaries by combining tracker snapshots with presence:

```ts
let latestActivity: RealtimeActivity[] = [];
const refresh = () => {
  const summary = aggregateRealtimeActivity(latestActivity, presence.snapshot('room-123'), {
    channel: 'room-123',
    names: ['typing', 'cursor'],
    label: member => typeof member.state.displayName === 'string'
      ? member.state.displayName : member.memberId,
  });
  renderTyping(formatRealtimeTypingSummary(summary)); // e.g. Alice and 2 others are typing.
  renderViewers(summary.viewers, summary.connectionCount);
  renderCursors(summary.active.flatMap(member => member.activities
    .filter(activity => activity.name === 'cursor')
    .map(activity => ({ memberId: member.memberId, label: member.label, state: member.state, data: activity.data }))));
};
const presence = createRealtimePresenceDirectory({ onChange: () => refresh() });
// Channel options:
// presenceScope: 'principal',
// onPresence: event => presence.apply(event),
// onActivityChange: entries => { latestActivity = entries; refresh(); },
// On connection teardown, clear presence.reset('room-123') as well.
```

`viewers` contains presence members regardless of activity; `active` contains
members with matching unexpired named signals. `viewerCount` counts member IDs,
while `connectionCount` sums represented connections. Set `presenceScope:
'principal'` consistently to let Gregale group connections by verified principal;
helpers never infer verified identity from presence state. Labels are untrusted
presentation data: render them as text. Cursor data remains application-defined.

Options support `names` (empty means no activity), `excludeMemberId`, and
`includeUnknownMembers` for senders such as `backend`. Unknown senders have zero
connections and are excluded from viewer counts. Results clone presence and
activity data. Members sort by ID for stable output. Typing text is an English
convenience helper (`maxLabels` 1–10, default 1); use `summary.active` for localized
text. A live named `typing` signal indicates typing regardless of its data shape;
clear it explicitly or allow expiry to end the indicator.

The presence directory accumulates snapshot chunks and publishes only complete
snapshots, while applying live joined/updated/left events. Call `reset(channel)`
on reconnect, unsubscribe, or teardown: activity lifecycle cleanup does not reset
this separately owned directory. Its `maxMembers` defaults to 1024, accepts
1–8192, and counts staged plus visible entries. Capacity rejection leaves state
unchanged; reset before a new snapshot when replacement staging would exceed the
limit. Helpers require no server migration.

Isolate activity for a backend-authorized audience:

```ts
const scopeChannel = realtimeActivityScopeChannel('document-123', 'section-2');
// Subscribe using channel: scopeChannel and the existing consumer options.
// onSubscribed receives actions that send signals only within this scope.
// onActivityChange receives only this scope's activity.
// Aggregate with channel: scopeChannel and presence.snapshot(scopeChannel).
```

The backend channel authorization callback receives `permission: 'read_activity'`,
`activity_parent_channel: 'document-123'`, `activity_scope: 'section-2'`, and the
canonical channel. Grant based on the verified principal's audience membership;
parent read access does not substitute for this separate authorization request.
Configure the callback before using scopes. Revoke/disconnect existing connections
when access changes; membership is checked again on reconnect.

Backend signals can use POST
`.../channels/document-123/activity-scopes/section-2/signals` with the normal
`realtimeBackendSignal` request. Scopes isolate signals and presence using separate
subscription channels, count toward the eight-channel connection limit, and use
existing activity lifecycle cleanup. Reset any independently owned presence
directory when leaving. Scope labels allow 1–64 UTF-8 bytes; the canonical encoded
channel must fit 256 bytes, so long parent names may need shortening. The
`__activity.` namespace is reserved and cannot be nested. Verified principal
grouping still uses `presenceScope: 'principal'` within each scope independently.

## Batch event publication

`EventsService.publishEventBatch({ requestBody: { events } })` accepts 1–100
stable-id envelopes in at most 1 MiB. Inspect every `results` entry: HTTP 200
can include rejected or unknown items. Retry with the original source/id/content;
duplicates retain their original receipt and do not create more deliveries.
See [batch publication](../../docs/event-driven.md#batch-event-publishing).

## Event retention health

The generated events service exposes `getEventRetentionHealth` (Node) or
`faas_sdk.api.events.get_event_retention_health` (Python). Filter receipt
observations by source/app and choose an expiry lookahead; storage utilization
remains account-wide. Recovery preflight also returns current receipt expiry
warnings and current retention holds. See [retention health](../../docs/event-driven.md#retention-health-and-expiry-alerts).

## Recovery receipt protection

Set `protect_receipts: true` when creating an event recovery job (Go:
`EventRecoveryRequest.ProtectReceipts`). Selected receipts remain held while
items are pending and the job is active, until its original 24-hour expiry.
Preview reserves nothing. Held receipts continue counting against account
storage limits. See [recovery protection](../../docs/event-driven.md#protect-receipts-during-bulk-recovery).

## Durable recovery outcomes

Existing recovery status and item reads prefer saved terminal results for the
exact admitted replay generation. `execution.source` is `recovery_result`, with
`recorded_at` and original `evidence_source`; job execution summaries include
`saved_results`. These results survive execution-history pruning until the
recovery job is pruned. Uncertain outcomes remain unknown. See
[terminal recovery results](../../docs/event-driven.md#durable-terminal-recovery-results).

Recovery webhook filters support `event_recovery.execution_finished`, separately
from admission completion. Its `EventRecoveryExecutionFinishedWebhookPayload`
contains saved terminal execution counts and `unresolved_count=0`; `all_succeeded`
refers only to queued executions. Recovery job `execution_finished_at` is capture
time, not webhook acknowledgement. Unknown evidence blocks capture. Only newly
created execution jobs with queued deliveries qualify. Update strict webhook
event-enum consumers before API rollout; existing webhook delivery retries and
dead-letter tools apply. See [ADR-915](../../docs/adr/915-recovery-execution-completion-notifications.md).

Existing recovery preview/create methods accept `parent_job_id` with
`mode=execution` to select only saved failed/dead-lettered deliveries from a
retained terminal recovery in the same app. Child creation requires a stable
`request_id` UUID: repeat it with the same normalized selection to return the same
retained child. Changed selections conflict, and the original audit reason wins.
Items expose historical `parent_job_id`/`parent_position` links. Changed or pruned
execution evidence is skipped at admission; newer replays are never substituted.
See [parent-scoped retries](../../docs/event-driven.md#retry-failures-from-one-recovery-job).

`EventsService.getEventRecoveryHealth` includes optional execution health for the
oldest retained unresolved terminal-admission jobs. `counts_complete=false`
marks lower-bound counts; prolonged waits measure time since admission completion.
See [execution recovery health](../../docs/adr/917-execution-recovery-health-alerts.md).

Use `EventsService.getEventRecoveryNotifications(jobID)` for a read-only report
of admission/execution capture and each selected receiver's current delivery.
Missing selection or pruned delivery evidence remains unknown; capture alone
does not prove acknowledgement. Retained dead deliveries link to independent
retry. See [notification delivery reports](../../docs/adr/918-recovery-notification-delivery-report.md).

### Recovery notification health

Recovery health responses include `notifications`, with separate admission and execution
counts for overdue, dead, unknown, and no-receiver jobs. Overdue requires known
unacknowledged delivery evidence at least 15 minutes after capture. Phase
`counts_complete` flags cover evidence uncertainty and the 50-job candidate bound;
partial observations cannot clear alerts. Use the notification report for receiver
details. New alert metrics are optional and require the notification health migration.

### Selective recovery notification retries

Use the job-scoped notification retry preview to choose receivers explicitly.
Submit a stable UUID request ID and targets containing kind, webhook ID, delivery
ID, and expected replay generation. The API revalidates each receiver and saves
queued or skipped decisions atomically. Repeating identical intent returns the
original decisions; use a new ID and current evidence for later failures.
Decisions expire with the recovery job. See the events documentation for limits
and migration rollout.

### Recovery notification retry history

Use the retry history methods to list saved request summaries or inspect one
request by ID. Detail preserves each receiver’s original queued or skipped
decision and shows its current retained delivery status with a separate read
timestamp. Missing deliveries are reported as unavailable; job pruning removes
the history.

Retry history detail also exposes `retry_outcome` for the original queued
generation, `retained_attempt_count`, `attempt_count_complete`, and optional
`completed_at`. Later retries do not establish an earlier generation's outcome.
Missing terminal evidence reports unknown; skipped decisions are not applicable.

History list summaries now include succeeded, failed, pending, and unknown
counts for the originally queued generations, aggregate `status`,
`evidence_complete`, and optional `completed_at`. Completion time requires
terminal evidence for every queued target. All-skipped requests are inconclusive;
later retries never establish an earlier generation's outcome.

Retry history lists accept an optional comma-separated `status` union such as
`failed,inconclusive`. Statuses must be distinct values from succeeded, failed,
pending, and inconclusive. The response includes `matched_count` and `totals`;
totals count all retained requests before filtering, including a separate count
of requests with incomplete evidence. No matches returns an empty list and
preserves full totals. Request detail and waiting do not accept this filter.

### App-wide recovery notification retry backlog

Use the notification retry backlog list method to discover original-generation
retry outcomes across retained recovery jobs for an app. Default statuses are
failed, pending, and inconclusive; an explicit status union can include succeeded.
`page_size` counts inspected jobs (default 5, maximum 10), not request rows.
Totals have `counts_scope: job_page` and cover scanned jobs before filtering.
Continue with `next_cursor` even when `requests` is empty, retaining the same
status selection. Each page is a fresh read-only snapshot. Returned detail and
retry-preview paths use existing recovery inspection endpoints.

Application-scoped producer keys: `EventsService.publishAppEvent({ slug, requestBody: { key: 'order-123-created', type: 'order.created', data: { order_id: '123' } } })` returns `duplicate` and the durable original receipt. Preserve app/key/content on retry. Subscribe to the returned `app.<UUID>` source. Deduplication lasts while the event receipt is retained; it does not guarantee exactly-once consumer effects.

Read-only producer-key reconciliation: `EventsService.getAppEventPublishStatus({ slug: 'my-app', key: 'order-123-created' })` returns the original retained receipt and paginated consumer `evidence`. `processing` and `accepted` both prove acceptance; accepted only means routing settled, not successful execution. `unavailable` cannot establish nonpublication and must not automatically trigger another publish. Follow `evidence.next_after` with `after` (default 100, maximum 200 recipients).

Read-only content verification: `EventsService.verifyAppEventPublication({ slug: 'my-app', requestBody: originalEvent })` returns `match`, `conflict` or `unavailable`. Matching uses normalized type/schema version and semantic JSON data; time and trace metadata are excluded. Match and conflict include the retained original receipt from the same comparison snapshot. No event is published, and unavailable must not automatically trigger publication.

To guard reconciliation against key reuse after pruning, pass `expectedAcceptedAt` to status or verification with the exact RFC3339 `accepted_at` string from the saved receipt. The independent `acceptance` field reports same_acceptance/replacement_acceptance/unavailable. A replacement can still have matching content; returned evidence belongs to the current retained acceptance, not the expected one. No timestamp rounding or writes occur.
Workflow declarations may pin a `version`; omitted versions in existing definitions mean version `1`. Scope a transition to its producer Operation and add `requires_milestones` to require named facts in the same app transaction. `tx.workflowTransition` automatically references the transaction's reported milestones. Gregale validates these references before the application commits and confirms the retained facts during publication. Current workflow states and history include `contract_version` and `evidence_milestones` so callers can inspect which contract accepted each transition. See [ADR-517](../../docs/adr/517-versioned-customer-workflow-contracts.md).

### Workflow blockers

Inside the customer Operation transaction callback, use `tx.workflowBlockers(workflow, instanceID, lockedRow.state, [{code: 'payment-pending', description: 'Payment confirmation is pending.', operation: 'fulfill-order'}])` to replace
the public blockers while preserving the current state. Check customer authorization
and read that state from the locked business row. An empty list clears blockers;
a later normal state report without blockers also clears them. Propagate errors
out of the callback. `workflow_instance.decision.blockers` exposes the latest
reported list alongside declared next actions. These reports do not enforce
business rules or grant execution authority.

Before upgrading, reinstall the SDK's additive customer Operation database schema
to add the blocker outbox columns. See [the Operations guide](../../docs/operations.md#report-workflow-blockers)
for bounds, replacement, revision, and publication semantics.

### Workflow attention queue

Use `client.workflowAttention({appID, scope: 'production', reason: 'blocked'})` to read one page of current retained blocked or stale workflows.
The response includes public business references, workflow snapshots, blocker
reasons and a continuation cursor. Workflow and target Operation filters narrow
the queue; customer routes use identity from credentials. Continue with the same
filters and `next_cursor`; refresh the first page for the latest view. See
[the Operations guide](../../docs/operations.md#find-workflows-needing-attention).

### Explain a cleared blocker

Use `tx.workflowBlockers(workflow, instanceID, lockedState, remainingBlockers, [resolution])` to attach an explicit public resolution fact to the transactional
blocker replacement. `OperationWorkflowBlockerResolution` includes the target
Operation, blocker code, explanation, and exact source Operation/report IDs and
revision. Current snapshots expose `operation_id`, `report_id`, and `revision`
for these references. The source must be a retained report within the same
customer, business reference, workflow run, environment and contract version;
it must contain the named blocker, which cannot remain in the replacement list.

Resolution facts survive outbox replay and remain in retained state history even
after a later snapshot replaces them. Reinstall the SDK's additive customer
Operation database schema before upgrading the adapter. See
[the Operations guide](../../docs/operations.md#explain-blocker-resolutions)
for bounds, source retention, publication recovery, and history reads.

#### Attention summaries and blocker age

```ts
const summary = await client.workflowAttentionSummary({
  appID, scope: 'production', groupBy: 'blocker_code', limit: 20,
});
```

Generated `OperationsService.summarizeAccountWorkflowAttention` and
`summarizePlatformTenantSelfWorkflowAttention` expose both API roles. Grouping
supports `workflow`, `blocker_code`, `target_operation`, and account-only
`customer`; both queue and summary options support `blockerCode`.
Totals cover all matching workflows independently of group pagination.

Install the updated `customerOperationReceiptSchema` before upgrading transactional
writers. Repeated blockers preserve optional `first_observed_at` until their
target/code is cleared. Legacy blockers retain unknown age; applications can
supply a known RFC3339 start. Upgrade every writer to preserve the counter's
blocker continuity. Ages are observation ages, separate from latest report time.

#### Business deadlines

Call `tx.workflowDeadline(workflow, instanceID, state, dueAt)` inside the business
transaction; a finite RFC3339 string sets/updates the deadline and `''` clears.
The SDK preserves current blockers and inherits due times on subsequent state,
transition, and blocker reports. Install the updated `customerOperationReceiptSchema`
and upgrade every writer. `workflowAttention({appID, scope, reason: 'overdue'})`
and `workflowAttentionSummary` expose overdue work, due times and durations.
Terminal workflows do not count as overdue.

#### Explicit business outcomes

Call `tx.workflowOutcome(workflow, instanceID, terminalState, code, description)`
after queuing its terminal transition and required milestones in the business
transaction. Terminal validation uses the pinned contract. The SDK preserves
blockers and deadline, inherits outcomes on later reports of the same state,
and drops them when state changes. Install the updated receipt schema.

Read with `client.workflowOutcomes({appID, scope, code: 'fulfilled'})` and
`client.workflowOutcomeSummary({appID, scope, groupBy: 'outcome'})`. Generated
`OperationsService` methods expose account and credential-scoped listing and
summary routes; only accounts can group by customer. Totals cover latest
retained terminal instances with explicit outcomes, counting each instance once.

### Workflow prerequisites

Use `tx.workflowDependencies(workflow, instanceID, state, [{subject_type, subject_id, workflow, instance_id, required_outcome_code}])` inside the business transaction to replace up to 16 direct workflow dependencies. Pass an empty list to clear them. Links stay within the same customer/application/environment; an optional required outcome distinguishes successful prerequisites from other terminal results. Other reports inherit current links. Apply the updated customer schema and upgrade all writers first. The existing workflow instance response includes `related_workflows` with retained states and explicit resolution statuses. See [workflow dependencies](../../docs/operations.md#workflow-dependencies) for complete examples and retention semantics.

### Dependency attention

Attention requests support `{dependencyStatus: 'waiting', requiredOutcomeCode: 'paid'}` and the `dependency` reason. The response includes `dependency_attention` references/statuses and summary counts `dependency_workflow_count` / `dependency_count`. Summaries also support `dependency_status` and `required_outcome_code` grouping. Both dependency filters must match the same unresolved reference. See [dependency-aware attention](../../docs/operations.md#dependency-aware-attention).

### Reverse dependency impact

Existing business milestones responses now include typed `workflow_instance.dependency_impact`: retained dependent workflows, required outcomes, prerequisite statuses, and affected-workflow counts. The list shows up to 100 items, affected sources first; counts cover all matches and `has_more` signals truncation. Unknown account-side prerequisites require an explicit customer; self reads always use the authenticated customer. See [reverse dependency impact](../../docs/operations.md#reverse-dependency-impact).

### Dependency root-cause tracing

Business milestones responses include typed `workflow_instance.dependency_trace` findings with linked reference paths and observed states. The trace follows unmet prerequisites, distinguishes cycles from shared workflows, and exposes missing reports, blockers, mismatched outcomes, staleness, and missed deadlines. Traversal is bounded; inspect `truncated` / `limits_reached` before treating coverage as complete. See [dependency root-cause tracing](../../docs/operations.md#dependency-root-cause-tracing).

### Workflow transition readiness

Use an authenticated operations reader to check a proposed transition:

```typescript
const result = await client.workflowReadiness({
  app_id: appID, scope: 'production', subject: {type: 'order', id: orderID},
  workflow: 'fulfillment', instance_id: runID, operation: 'ship-order',
  from_state: 'waiting', to_state: 'shipping', milestones: ['shipment-created'],
  state_revision: revision, contract_version: 1,
});
```

Inspect `readiness.ready`, denial reasons, missing milestones, unmet prerequisites, and advisories. Account readers use the account readiness endpoint with an explicit customer selector. Planned names are not committed evidence; business-row checks, authorization, and transaction-time workflow/milestone validation still apply. See [workflow transition readiness](../../docs/operations.md#workflow-transition-readiness).

### Guard a transition inside the business transaction

After locking the business row, await the guard before writing:

```ts
await tx.guardedWorkflowTransition(
  {app_id: appID, scope, subject, workflow, instance_id: instanceID,
   operation, from_state: row.state, to_state: 'approved',
   state_revision: row.workflow_revision, contract_version: contractVersion},
  [{name: 'approved', payload}],
  request => customerClient.workflowReadiness(request),
);
// Business writes use tx.query here.
```

Use a client authenticated as the transaction's customer. A
`CustomerOperationReadinessError` exposes `.response`; all guard failures prevent
commit even if caught. Await each guard sequentially inside the callback. Existing
contract and actual payload validation still runs before commit.

### Business decision evidence

`tx.businessDecision('approval-decided', {workflow: 'order-approval', instance_id: runID, code: 'manual-review-approved', description: 'An authorized reviewer approved the order.', rule_id: 'manual-approval', rule_version: '2026-10'})` queues a bounded explanation with the business transaction. Declare the milestone payload schema and bind its workflow step to `/decision/instance_id`. It uses existing precommit validation and outbox replay; no schema installation is needed. See [business decision evidence](../../docs/operations.md#business-decision-evidence) for declaration and history details.

### Versioned policy requirements

Workflow transitions can declare `requires_policies` with a milestone, rule ID, exact rule version, and decision code. Transactional readiness guards derive planned decisions from actual milestone payloads, and precommit validation requires matching evidence for the same workflow instance. See [policy requirements](../../docs/operations.md#versioned-business-policy-requirements).

### Business state reconciliation

Reconciliation transaction helpers compare the locked application row with customer-scoped workflow history and queue a fresh explicit snapshot plus discrepancy evidence when needed. Business revisions stay separate from SDK report counters. Ahead/version conflicts record diagnostics without refreshing state. Declare the reconciliation milestone schema and bind its step to `/reconciliation/instance_id`. See [reconciliation usage](../../docs/operations.md#business-state-reconciliation).

### Transition-specific prerequisites

Declare `requires_dependencies` on a transition to select workflow names from the current instance's reported links. Omitted selects all links; `[]` selects none. Missing required links are structured readiness failures. SDK guards apply the selected edge's requirements. See [prerequisite usage](../../docs/operations.md#transition-specific-business-prerequisites).

### Business action previews

Read-only action preview helpers return current-state candidates with revision/version and all transition requirements. Candidates use an empty evidence plan. Use the transaction readiness guard with actual facts and locked business rows before performing an action. See [preview usage](../../docs/operations.md#business-action-previews).

### Business invariant reports

Invariant helpers queue a typed check fact and targeted blocker update with the business transaction. Failed and unknown checks block their target Operations; passed checks clear only their stable invariant code. Supply the complete locked blocker head and chain returned blockers for multiple checks. Guards also consider pending invariant blockers. See [invariant usage](../../docs/operations.md#business-invariant-reports).

### Required invariant evidence

Transitions may declare `requires_invariants` with a milestone, stable code, and exact version. Guards derive check plans from actual invariant payloads. Passing evidence must match the source state, instance, and target action and accompany the transaction. See [required invariants](../../docs/operations.md#required-invariant-evidence-per-transition).

### Business effect evidence

Effect helpers record pending, failed, or confirmed business facts with a reference and optional amount/currency. Transition `requires_effects` requirements need matching confirmed evidence. Guards derive plans from actual effect payloads. External effects still need application idempotency and verified confirmation. See [effect usage](../../docs/operations.md#business-effect-evidence).

### Compensation workflows

Compensation helpers record required, pending, failed, or confirmed reversal observations linked to a retained confirmed effect. Source ownership/app/environment are checked before commit and publication. Applications execute reversals and report workflow state explicitly. See [compensation usage](../../docs/operations.md#compensation-workflows).
### Transactional realtime publishing (preview)

`insertRealtimeCommitEvent(client, { endpointId, channel, data, metadata? })`
queues a retained JSON text frame in the same PostgreSQL transaction as your
business write. Use a transaction-bound client; the helper does not begin or
commit transactions, install tables, or publish over HTTP. Rollback removes the
event. Gregale's managed Commit dispatcher performs publication and retries.

`listRealtimeCommitDeliveries(db)` and `getRealtimeCommitDelivery(db, eventId)`
read payload-free delivery metadata from your database. A published sequence is
a decimal string. `retryRealtimeCommitEvent(db, eventId)` clears a repaired
blocked event without resetting identity. A missing inspection row can mean
cleanup; it does not establish successful publication.

Requires explicit customer outbox installation, an enabled managed Commit
source and the transactional realtime operator preview. Supports v2 retained
subscribers and existing schema/reducer validation; published does not mean
client received. See [setup and example](../../docs/ops/realtime.md#transactional-publishing-preview).
### Managed realtime access controls (preview)

Use `revokeRealtimeChannelAccess({ slug, id, principal }, channel)` to deny a
user's channel subscriptions across enabled nodes, including private activity
scopes. `restoreRealtimeChannelAccess(target, channel)` removes the deny; your
application must still authorize membership and clients must subscribe again.

`revokeRealtimeSessions(target)` disconnects older OIDC sessions and prevents
their tokens from reconnecting. A fresh token needs an integer signed `iat`
strictly after the cutoff. This logs out older sessions; permanent bans remain
an application authentication decision. `RealtimeService.getManagedRealtimeAccess`
reads the persisted controls. API success confirms persistence, not fleet
convergence. See [setup](../../docs/ops/realtime.md#principal-access-controls-preview).

### PostgreSQL table-change feeds

After explicit owner installation of `pkg/commit/realtime_changefeeds.sql`, use
`installRealtimeChangefeed(ownerTx, 'public.orders', { name: 'order_status',
endpointId, channel: 'orders:', tenantColumn: 'tenant_id',
columns: ['id', 'tenant_id', 'status'] })` to capture row inserts, updates and deletes
through the transactional realtime outbox. Primary key and tenant fields must be
explicitly allowed. Unchanged selected fields emit nothing. Tenant moves require
an explicit delete/insert. `removeRealtimeChangefeed(ownerTx, table, name)` stops
future capture. Writer privileges and the complete 4096-byte event bound apply;
capture failures roll back the business write. See the operator guide for setup,
authorization, schema migration and retention requirements.

### Realtime over SSE

`subscribeRealtimeSSE({ url, channel, token: () => getFreshToken(), after, signal })`
is exported from both the main and browser SDK entries. It yields retained channel
messages with decimal-string sequences and base64 payloads. It uses fetch with
Authorization headers, reconnects transient failures and refreshes tokens on each
attempt. Requires resume preview, OIDC and explicit allowed origins for browsers.
`RealtimeSSEError` terminates authentication/access/protocol failures and reports
`resync_required` when callers must rebuild from an application snapshot. Delivery
may repeat; consumers must apply events idempotently. This receive-only transport
does not provide presence, signals, inboxes or server-side ACK checkpoints.

### Channel-event webhooks

Subscribe an app webhook explicitly to `realtime.channel.published`, then use
`WebhooksService.setRealtimeChannelWebhookFilter({ slug, id, requestBody: {
endpoint_id, channel, metadata } })`. The corresponding get/delete methods inspect
or remove the exact channel binding. Metadata uses all-key equality matching.
Existing webhook signing, delivery history and retries apply; deduplicate payload
`event_id`, and treat sequence as a decimal string. Removing a binding stops future
capture without recalling queued payloads. Empty catch-all event filters do not
opt in. See the operator guide for the required platform migration.

### Subscription tickets

Backends can call `RealtimeService.issueRealtimeSubscriptionTicket({ slug, id,
requestBody: { principal, channels, ttl_seconds } })` after authenticating the user
and authorizing every exact channel. Return only the short-lived ticket to clients;
never expose backend platform credentials. Requires operator ticket/resume preview.

Browsers can use `createBrowserRealtimeTicketSocketFactory(getFreshTicket)` with
the existing v2 channel consumer; the provider runs on every reconnect. For SSE,
use `getFreshTicket` as the token provider. Tickets allow retained subscriptions
only, expire within five minutes and obey principal session/channel revocation.
Named durable subscriptions, presence, signals, inboxes and direct messages are
unsupported. See the operator guide for migrations, endpoint policy and activation.

### Automatic WebSocket-to-SSE fallback

`consumeRealtimeChannelWithFallback({ url, channel, token: getFreshCredential,
cursorStore, onMessage, onTransportChange, signal })` provides receive-only retained
subscriptions from both SDK entries. It starts with WebSocket, then switches to
SSE after two failed connection/subscription attempts. Both transports share a
numeric cursor store and decoded message callbacks; processing precedes cursor
save. Node callers supply an authenticated `webSocketFactory`. Fresh OIDC tokens
or subscription tickets are obtained on reconnect. Explicit authorization/protocol
failures are terminal, and optional `onResync` handles expired history. Fallback
stays on SSE until a new invocation. Presence, signals, inboxes, metadata filters
and named durable subscriptions are unsupported; see the operator guide.

For a named durable consumer, your backend can read the latest actual advancing
ACK with `RealtimeService.getRealtimeChannelAcknowledgement({ slug, id, channel,
principal, subscription })`. The response separates the decimal-string checkpoint
from nullable `acknowledged_sequence` / `acknowledged_at`. Initial baselines and
resets are not processing receipts. This retains only the latest ACK, not a receipt
for every message; do not infer processing of skipped or filtered messages.
Explicitly select `realtime.channel.acknowledged` on an enabled app webhook to
receive signed receipts across that app's endpoints. Runtime and migration checks
remain pending; see the realtime operations guide.

Backend workers can share channel work with consumer groups. Create a group via
`RealtimeService.createRealtimeConsumerGroup`, then run
`consumeRealtimeConsumerGroup({ slug, id, channel, group, worker, signal,
onMessage })`. The helper claims one message at a time, renews its lease while the
handler runs, completes after success, and requests bounded retry on handler
failure. Configure account API credentials on the backend; this helper is not
exported from the browser entrypoint. Its handler receives a cancellation signal
on shutdown or lease loss and must use idempotent effects. Network/auth/lease
errors terminate the helper; restart with backoff. Generated clients expose status,
dead-letter replay and explicit retention-gap recovery. See ADR-1030 and the
realtime operations guide for capacity bounds and migration requirements.

For a least-privilege worker, issue a group-bound `grw_` credential with `status`,
`claim`, `renew`, `complete` and `retry`. Use
`createRealtimeConsumerGroupWorkerClient({ groupId, credential })` and
`consumeRealtimeConsumerGroupWithCredential({ client, signal, onMessage })`.
The credential may be an async provider called per request to support rotation.
This client keeps worker authentication isolated from global account credentials
and does not inherit account headers/cookies. A worker can settle only its own
credential's leases. Rotation preserves that ownership; revocation/expiry stops
further settlement/renewal. Administrative grants are explicit and separate.

Publish from a backend with a channel-scoped `grp_` credential:

```ts
const publisher = createRealtimePublisherClient({
  endpointId,
  credential: getFreshPublisherCredential,
});
const result = await publisher.publish('jobs', {
  event_type: 'job.progress',
  data_base64: Buffer.from(JSON.stringify({ progress: 50 })).toString('base64'),
}, 'job-123-progress-50');
```

Import `createRealtimePublisherClient` from the Node entrypoint. Account admins
issue the credential with exact channel/event grants through the generated
`RealtimeService` APIs. The isolated client obtains a fresh credential per request
and does not inherit account headers or cookies. Reuse the same idempotency key
and decoded content after uncertain outcomes. Receipts are bounded to 1024 per
credential for 24 hours; changed content conflicts. A partial response still
represents a durable event. See ADR-1032 and the operator guide before activation.

Consumer-group worker helpers accept `failureCode: (error, message) =>
'upstream_unavailable'` to record a safe diagnostic code after handler failure.
Return 1–64 printable ASCII bytes; omit raw exception messages and secrets.
The default remains `handler_failed`. A throwing/invalid callback terminates the
helper and leaves the claim to expire. Both account and scoped-worker helpers
support this option. Direct generated Node/Python APIs already accept
`failure_code` in retry settlements.

Subscribe an app webhook explicitly to `realtime.consumer_group.dead_lettered`
for signed exhaustion alerts with group/channel/sequence, attempts and failure
code. Alerts commit with the dead transition, use existing webhook retries and
exclude message payloads. Lease-exhaustion alerts wait for the next claim.
Replay remains explicit; another exhausted replay emits a new alert. See ADR-1033
and the operator guide for migration requirements and inspector links.

For concurrent group processing, set `concurrency: 8, batchSize: 8` on either
consumer-group helper. The default remains one handler using single claims.
Helpers claim only available handler slots, renew leases independently and settle
messages separately. `concurrency` permits 1–32 and `batchSize` 1–16; the group-wide
lease and outstanding snapshot limits still apply. Completion can be out of
order, so effects must be idempotent and handlers should honor their abort signal.

Direct clients can use `RealtimeService.claimRealtimeConsumerGroupBatch` or the
isolated worker client's `claimBatch(limit)`. Generated Python APIs expose both
account and worker batch endpoints. A 200 response may contain an empty or partial
`claims` array with `history_unavailable` or `capacity_blocked`. Process returned
claims before fixing that blocker. Do not retry uncertain claims automatically;
unobserved leases expire. See ADR-1034 and the operator guide.

Pause consumer-group claims with `RealtimeService.pauseRealtimeConsumerGroup`
and resume with `RealtimeService.resumeRealtimeConsumerGroup` (account admin
scope and MFA). An isolated worker client exposes `pause()` and `resume()` when
its credential has the corresponding explicit grant. Responses contain `paused`
and nullable `paused_at`; group status and batch results also expose `paused`.

Existing handlers can finish, renew or retry during pause. Helpers keep polling
empty claims and automatically claim again after resume. Pause preserves the
cursor and retry counts, but lease clocks and channel retention continue. It does
not cancel already-issued work. Generated Python clients expose the same controls.
Apply the ADR-1035 migration before activation; runtime checks remain pending.

Use `drainRealtimeConsumerGroup({ slug, id, channel, group, timeoutMs: 60_000 })`
to stop new claims and wait for active leases to clear. Scoped callers use
`drainRealtimeConsumerGroupWithCredential({ client, timeoutMs: 60_000 })` with
explicit `drain` and `status` grants. Import helpers from the Node entrypoint.
The default timeout is 30 seconds and covers requests and polling; caller abort
signals are supported. Timeout/cancellation never resumes the group.

Direct worker clients expose `drain()`, and generated Node/Python APIs expose drain
initiation and group status. Resume explicitly after deployment. Drained status
means zero unexpired leases; it does not prove expired handlers stopped external
effects. Stop workers, honor handler cancellation and keep effects idempotent.
See ADR-1036 for migration requirements and runtime validation status.

Consumer-group helpers now default to `waitSeconds: 10` for long-poll claims.
Set 0 to retain immediate polling, or choose an integer up to 20. Both single and
batch requests stop waiting when work or pause/drain/blockers are observed;
existing empty-result backoff remains. Direct worker clients support
`claim(worker, waitSeconds)` and `claimBatch(limit, worker, waitSeconds)`.
Generated Node/Python models expose `wait_seconds`, defaulting to 0.

Set proxy/client timeouts above the wait plus request overhead. The preview checks
PostgreSQL every 500 ms with no transaction held between attempts; it reduces
HTTP traffic while retaining database polling. There are 64 waiting slots per
apid process. Cancellation may leave an unobserved committed lease to expire;
do not automatically retry uncertain claims. See ADR-1037.

Configure sampled consumer-group lag alerts with
`RealtimeService.configureRealtimeConsumerGroupLagAlert({ slug, id, channel, group,
requestBody: { enabled: true, unassigned_threshold: '100', oldest_pending_seconds:
60, dead_letter_threshold: 1, cooldown_seconds: 300 } })`. Read policy and nullable
observations with `getRealtimeConsumerGroupLagAlert`. Writes require account admin
scope and MFA; scoped worker credentials do not configure policies. Generated
Python exposes the same operations.

Explicitly subscribe app webhooks to `realtime.consumer_group.lag_exceeded` and
`realtime.consumer_group.lag_recovered`. Any configured threshold can breach;
zero disables a metric. Cooldown limits ongoing reminders; actual transitions
alert promptly. Identical PUTs preserve state and changed policies rearm. Paused
and draining groups remain monitored. Apply the ADR-1038 migration before activation;
runtime checks remain pending.

Create ordered groups with `ordering_metadata_key: 'job_id'` and
`dead_letter_policy: 'block'` (default) or `'skip'` in
`RealtimeService.createRealtimeConsumerGroup`'s request body. Policies are immutable.
Publish a consistent exact metadata value; missing/empty values share a serialized
bucket. Worker concurrency runs different keys in parallel while leases/retries
hold later same-key work. No new worker permission or helper setting is required.

Group status exposes policy and blocked-ready count; batch `ordering_blocked`
is normal backpressure and helpers keep polling. Under block, repair/replay the
dead predecessor; under skip, replay can occur after successors completed and
conflicts while another same-key lease exists. Lookahead remains bounded to 128
snapshots, and external effects still need idempotency/fencing. Generated Python
supports the same creation/status fields. See ADR-1039 and apply its migration
before activation; runtime checks remain pending.

Worker handlers receive `context.claim.group_id` and
`context.claim.fencing_token` on every new lease. Pass these to downstream storage
and atomically reject lower fences within the authorized group UUID and resource
scope. Use decimal strings or `BigInt(claim.fencing_token)`, never Number. Renewals
keep the token; retries/replay receive a larger token on their next claim. Group
status exposes `last_fencing_token`. Generated Python claims expose the same fields.

Scope fences per entity for ordered processing, or per message sequence; one
shared high-water mark across independent entities would reject valid concurrent
work. The number does not authorize API operations and downstream enforcement is
required. A newer claim alone does not invalidate an old downstream writer until
the store registers/enforces the higher token. Idempotent effects remain necessary.
Apply ADR-1040's migration and upgrade all API replicas/workers before relying on
fencing. Legacy leases have no retroactive fence; runtime checks remain pending.

Consumer-group dead letters now support opt-in retention. Set
`dead_letter_retention_seconds` at creation or use generated
`configureRealtimeConsumerGroupDeadLetterRetention` with `retention_seconds`:
zero keeps indefinitely, otherwise choose one hour to 30 days. Ordered groups
require `skip`. Account admin scope and MFA are required to configure it.
`getRealtimeConsumerGroupDeadLetterRetention` returns the policy, fixed 300-second
warning grace and cumulative decimal-string `expired_dead_letters` count.

Subscribe to `realtime.consumer_group.dead_letter_expiring` for signed expiry
warnings. Cleanup waits at least five minutes after the persisted warning decision,
including after outages, but does not wait for webhook delivery. Dead-letter
responses include nullable `retention_started_at`, `expires_at`, `warning_at`, and
`cleanup_after`. Replay protects a snapshot if it commits before cleanup. Paused
and draining groups still expire dead work. Python exposes the same generated API.
Migration/runtime validation remains pending; see ADR 1041.

Consumer-group retry policies are opt-in. Use generated
`configureRealtimeConsumerGroupRetryPolicy` with `enabled: true`,
`base_delay_seconds: 2`, `max_delay_seconds: 60`, `jitter: true`, and optional
`permanent_failure_codes: ['invalid_payload']`. This requires account admin scope
and MFA. `getRealtimeConsumerGroupRetryPolicy` returns the policy and nullable
`next_retry_at`, also visible in the inspector. Python exposes the same API.

Set `useGroupRetryPolicy: true` in the Node consumer helper (account or scoped
worker) to use server backoff. Its default retains the previous one-second delay.
An explicit `retryAfterSeconds`, including zero, overrides backoff; permanent codes
and attempt exhaustion still dead-letter immediately. Backoff doubles after each
failed attempt, caps at 300 seconds, and optionally samples from half to full delay.
Changes do not reschedule existing retries. Lease expiry retains existing behavior.
Ready settlements return `available_at`; pause/order constraints can delay claims
further. Migration/runtime validation remains pending; see ADR 1042.

Bulk dead-letter recovery now offers `previewRealtimeConsumerGroupDeadLetters`
and `bulkReplayRealtimeConsumerGroupDeadLetters` in the generated account client.
Both require app ownership and MFA; preview uses read scope and replay uses
deploy-write scope. Preview body example:

```json
{"failure_codes":["invalid_payload"],"after_sequence":"0","limit":32}
```

Preview returns `group_id`, ordered `items`, and `has_more`. Select items deliberately
and replay their exact `sequence` and `dead_since` values:

```ts
const selection = {
  group_id: preview.group_id,
  items: preview.items.map(({ sequence, dead_since }) => ({ sequence, dead_since })),
};
```

Pass this as the generated bulk replay request body. Preserve timestamp strings;
converting through JavaScript Date loses microsecond precision and can make a
selection stale. Each batch accepts 1–32 unique sequences and returns outcomes:
`replayed`, `ordering_blocked`, `stale`, `not_dead`, or `missing`. Missing may mean
retention cleanup, but the API cannot infer the cause. An old preview never replays
a recreated group or a later dead incarnation. Use the last sequence as the next
preview's exclusive `after_sequence` when `has_more` is true. Python exposes the
same endpoints and preserves datetime microseconds.

Successful replays reset attempts; pause/drain and ordering still govern claims.
The next claim gets a fresh fencing token. Unexpected errors roll back the batch;
recognized per-item conflicts allow eligible items to proceed. Do not automatically
retry uncertain responses. Runtime validation remains pending; see ADR 1043.

Consumer groups can opt into delivery deadlines at creation with
`deadline_metadata_key: 'deliver_by'`. Publish a metadata value such as
`deliver_by: '2026-10-12T12:00:00Z'`. Missing/empty values have no deadline;
malformed timestamps become terminal `invalid_delivery_deadline` failures.
Ordered groups require `dead_letter_policy: 'skip'`. The field and each assigned
snapshot's deadline are immutable; existing groups retain no deadline behavior.

Claims expose nullable `delivery_deadline`, and leases/renewals cannot extend past
it. The Node consumer helper aborts the handler signal and renewal at the deadline;
handlers must honor cancellation and guard external effects. Server checks are
authoritative; local clock skew can cause early cancellation. Python claims expose
the same timestamp. Expired work becomes a `delivery_expired` dead letter during
claim/replay maintenance, with signed `realtime.consumer_group.delivery_expired`
webhooks and cumulative `expired_deliveries` status/inspector counters. Invalid
values also increment this rejection counter; successful completions do not.

There is no background expiry timer: unassigned or untouched work is observed on
maintenance. Dead snapshots still consume capacity until compatible retention or
explicit group retirement. Replay cannot extend a frozen deadline: single replay
conflicts, bulk replay reports `deadline_expired` or `invalid_deadline`. Ordinary
failed work with a future deadline can still replay. Publish corrected replacement
work deliberately when needed. Migration/runtime validation remains pending;
see ADR 1044.

Consumer-group claim rates are opt-in and shared across all workers/API replicas.
Use generated `configureRealtimeConsumerGroupClaimRate` with
`{ rate_per_second: 10, burst: 4 }`; rate zero disables throttling. Rates range
from 1–1000 claims per second and burst from 1–32. Account admin scope and MFA
are required to configure; `getRealtimeConsumerGroupClaimRate` uses read scope.
Python exposes the same generated routes.

Each successful new lease consumes credit; renewals, settlement and replay itself
do not. Batch responses include `rate_throttled` and `retry_after_milliseconds`
and may contain partial claims. Single claims return a specific 429 problem code,
`realtime_group_rate_limited`, with `Retry-After` and `retry_after_seconds`. Shared
Node account/scoped-worker helpers wait for these hints and continue processing
admitted work. Batch mode supports finer pacing than the rounded HTTP seconds
used by the single route. Hints do not reserve credit from competing workers.

The inspector and policy response expose `throttled_claim_requests`, a cumulative
decimal-string count of committed admission decisions stopped by rate, not messages.
Identical policy writes preserve credit; changed policies clamp existing debt to
the new burst window. Disable/re-enable grants the configured burst. Existing
credential request budgets, lease limits, pause/drain and ordering still apply.
Migration/runtime validation remains pending; see ADR 1045.

Consumer groups can now select event types and exact metadata at creation:

```json
{
  "name": "billing",
  "after_sequence": "0",
  "filter": {
    "event_types": ["invoice.created"],
    "metadata": {"region": "eu"}
  }
}
```

`event_types` matches `metadata.event_type`, rather than the retained message's
created/updated/deleted lifecycle `event`. Types use OR; every metadata predicate
and the type condition must match. Missing metadata does not equal an empty value.
Empty filters match everything. Limits: 32 unique types of up to 128 bytes, eight
metadata predicates using existing metadata bounds, and 8192 encoded filter bytes.
Filters are immutable; create a new group with an explicit starting sequence to
change selection. Generated Node/Python creation models expose the same filter.

Nonmatching history advances the group cursor without consuming snapshot capacity,
claim credit or worker attempts. Status and the inspector expose `filter` and the
cumulative decimal-string `filtered_messages` count. Matching work retains ordering,
deadline, retry and replay behavior. A missing-history gap still requires explicit
recovery; lag metrics describe raw unassigned channel events until scanning.

Each batch transaction scans at most 128 assignment events and returns
`filter_scan_limited` when it reaches that budget, possibly alongside partial claims.
Progress is committed; poll again to continue. Single claims return 204 when no
matching claim was admitted. Node helpers keep polling without treating filtering
as a failure. Pause/drain, full capacity and rate limits can delay cursor scanning.
Migration/runtime validation remains pending; see ADR 1046.

Worker health reporting is opt-in. Set `heartbeat: true` on the Node account or
scoped consumer helper to report ready every 15 seconds while idle or processing.
Scoped credentials need the new explicit `heartbeat` permission. The isolated
worker client also supports `heartbeat({ ready: false, status_code: 'maintenance' })`
for custom reporting. Account clients use generated `heartbeatRealtimeConsumerGroup`
with an explicit worker name; `getRealtimeConsumerGroupWorkerHealth` reads status.
Generated Python routes expose the same models and scoped heartbeat request.

Timeout is 60 seconds. Ready and live are separate; readiness is self-reported,
while live requires a fresh report and eligible scoped credential. Use a separate
credential per process for independent identities; sharing aggregates reports.
Health reporting never renews leases or consumes claim-rate credit, but existing
credential request budgets apply. Reporting errors stop an opted-in Node helper;
shutdown cancels reporting and natural timeout later records loss.

Signed `realtime.consumer_group.worker_lost` and `worker_recovered` webhooks report
liveness transitions. Recovery does not imply ready=true. The inspector shows
last-seen/status and pending groups with no ready reporting workers. Non-reporting
workers remain unobserved. At most 32 identities are tracked; stale lost rows can
be replaced under capacity pressure or pruned after a day, without a later recovery
backfill. Migration/runtime validation remains pending; see ADR 1047.

Consumer-group processing helpers accept `settlementRetries: 2` (0–3; default 0).
When enabled, terminal settlements use a UUID receipt key and retry transport errors
and HTTP 408/500/502/503/504 without rerunning the handler. Batch retries reuse one
key and the entire ordered batch. Renewals are not automatically retried; cancellation,
auth, validation, conflicts and 429 stop retries. Deploy the settlement-receipt migration
and API changes before opting in. Receipts last 24 hours, at most 1024 per group;
full capacity rejects new keyed settlements before mutation. Generated Node/Python
settlement bodies expose `idempotency_key` for manual retry policies. Batch keys belong
on the outer body, never individual items. Saved results report the original outcome.

For event deduplication, create a consumer group with
`deduplication_metadata_key: 'event_id'` and optionally
`deduplication_window_seconds: 3600`. Settings are immutable; missing/empty IDs process
normally. Group status exposes `duplicate_messages` and `deduplication_keys`.
Duplicate copies skip assignment while retries and dead-letter replay remain available.
The store holds at most 4096 IDs per group without evicting live entries. Processing
helpers handle single `realtime_group_deduplication_full` errors and batch
`deduplication_blocked` hints while existing snapshots remain claimable. Deploy the
migration and upgrade all API replicas before enabling this policy. Deduplication
covers a finite assignment window and does not guarantee handler side-effect uniqueness.

`RealtimeService.exportRealtimeConsumerGroupDeadLetters` downloads a JSONL `Blob`.
Its first line is `RealtimeDeadLetterExportManifest`; record lines follow
`RealtimeDeadLetterExportRecord`. Read the manifest for `has_more`, then resume with
`groupId`, `throughSequence`, `snapshotAt` and `afterSequence: next_after_sequence`,
retaining failure-code filters. Default limit is 32, maximum 128. Verify the complete
record count before advancing. Export is read-only and live: replay/cleanup can remove
records between pages. Save files outside Gregale for longer retention. Account read
scope, MFA and history preview are required; scoped workers cannot export.


### Background dead-letter replay jobs

Use `POST .../groups/{group}/replay-jobs` with `rate` (1–32), optional `failure_codes`, `after_sequence` (exclusive) and `through_sequence` (inclusive). Creation captures current failures only, up to 128; an empty selection is invalid. `GET .../replay-jobs` shows active jobs first and recent terminal jobs. `POST .../replay-jobs/{job}/{action}` accepts `pause`, `resume` or `cancel`. Account ownership and MFA apply; mutations require deploy-write scope. The inspector offers the same controls.

Rate is an upper limit per job; multiple jobs have independent limits. Known outcomes include replayed, stale, missing, not_dead, ordering_blocked, invalid_deadline and deadline_expired. Skips require a new recovery job. Safe processing errors and cumulative counts appear in status; database errors retry without advancing progress. Cancellation does not undo completed replay. Inspect recent jobs before retrying an uncertain creation response. Disabled endpoints suspend execution; group claim pause/circuit/rate settings still govern subsequent processing.

Apply migration `20261011190000001_realtime_dead_letter_replay_jobs.sql` before enabling jobs, with all API replicas upgraded. Cancel active jobs before downgrade. Migration and runtime/UI checks are currently deferred.


### Replay-job notifications

Create or update an app webhook with event filters `realtime.replay_job.completed`, `realtime.replay_job.cancelled` and/or `realtime.replay_job.processing_failed`. Existing webhook signing, retry policy, delivery status and dead-letter inspection apply. The inspector links to app webhook configuration.

Completion and cancellation notify once per transition. The processing warning notifies once per job when cumulative recorded processing errors first reach three; the job continues retrying. Known skips are not processing errors. No backfill for past transitions or newly added subscribers. Deduplicate the payload's `event_id` across delivery retries and do not assume delivery order.

`RealtimeReplayJobWebhookPayload` documents recovery counts, safe error diagnostics and a relative `inspector_path` to resolve against your console origin. Payloads exclude failed messages and credential secrets. Job history can be pruned after notification. Migration `20261011200000001_realtime_replay_job_notifications.sql` is required and currently unapplied.

### Reviewed dead-letter recovery

Call `RealtimeService.createRealtimeReplayDryRun` with the same filter/rate request as job creation. Poll `listRealtimeReplayJobs` until the dry run reaches `completed`, then inspect its eligible/skipped counts and outcome timestamps. Call `controlRealtimeReplayJob` with the report UUID and `action: 'start'` to replay only reviewed eligible identities. The response is the linked real replay; repeated start returns that same job while retained. Replays recheck identity, deadlines and ordering, so reviewed eligibility does not guarantee replay. Dry runs share quotas with real jobs and emit no replay lifecycle webhooks.

### Retained channel message TTL

Single retained-channel publishing accepts optional `ttl_seconds` (1–86400; zero
or omitted means no expiry). Generated request models expose it. Retries with an
idempotency key must use the same TTL and preserve the original deadline; edits
inherit that deadline. WebSocket retained consumers skip expired positions, persist
their cursors and ACK them without calling `onMessage`. Message DTOs expose optional
`expiresAt`; REST history uses `expired: true` with no payload/metadata.

Low-level `subscribeRealtimeSSE` yields entries with `checkpoint: true` for expired
positions. Advance your cursor without treating these entries as application
messages. The receive-only fallback handles this automatically. Upgrade clients
and the server/private readers after deploying migration 20261011220000001 before
enabling TTL publishing. TTL affects delivery selection, not already delivered
state, retention deletion or consumer-group job processing. TTL publishing covers account-authenticated single and batch retained writes. Runtime checks remain
deferred.

### Batch TTL

Retained batch requests accept `ttl_seconds` as a default and
`messages[i].ttl_seconds` as an override. Omitted/null overrides inherit; zero
turns expiry off for that item. Values are bounded by 86400 seconds. A default of
60 with overrides omitted, 10 and 0 produces one-minute, ten-second and unlimited
lifetimes. Deadlines persist atomically and survive idempotent retries; changing
effective lifetimes conflicts with the batch ID. Existing expiration checkpoints
handle skipped positions. Deploy the TTL companion-table migration before enabling
batch expiry; runtime checks remain deferred.

Scheduled realtime publishing accepts optional `ttl_seconds` (0–86400; zero disables expiry). Each occurrence starts its message lifetime at actual publication. Rescheduling and retries preserve the schedule setting and cannot extend a committed message deadline.


For transactional realtime TTL, first install the optional customer outbox TTL
upgrade (`pkg/commit/realtime_ttl_upgrade.sql` for existing databases). In your
existing transaction on the same connection:

```ts
await setRealtimeCommitTTL(tx, 60);
await insertRealtimeCommitEvent(tx, { endpointId, channel, data }); // inherits 60s
await insertRealtimeCommitEvent(tx, { endpointId, channel, data, ttlSeconds: 0 }); // no expiry
```

Import both helpers from the SDK. Call the default setter after `BEGIN`; it clears
on commit or rollback and also covers changefeed captures on that connection.
TTL is 0–86400 seconds and begins at retained publication by the relay. Retried
receipts preserve the original deadline. The helpers never control transactions.

Scoped publisher `publish` messages accept `ttl_seconds` (0–86400; zero disables
expiry). Credential issuance can set `max_message_ttl_seconds` (zero means
uncapped); a positive cap requires a positive message TTL at or below it.
Omitted or zero message TTL is denied when capped. Issue/rotation `ttl_seconds`
is the credential's own lifetime, independent of message expiry. Rotation
preserves the message cap. Keep message TTL unchanged when retrying a key.

Observe expired retained positions without processing expired payloads:

```ts
const expiry = new RealtimeExpiryDiagnostics();
// Pass this option to consumeRealtimeChannel, subscribeRealtimeSSE or consumeRealtimeChannelWithFallback:
const diagnostics = { onMessageExpired: expiry.record };
const counts = expiry.snapshot(); // websocketCheckpoints / sseCheckpoints
```

Import `RealtimeExpiryDiagnostics` from the SDK. The optional synchronous observer
receives only `code: "message_expired"`, channel, decimal-string sequence and
transport. It runs after protocol validation, before cursor persistence/ACK.
Observer errors are isolated; never use the hook for business effects or blocking
work. Reconnects can observe a position again. Counts are process-local and
saturate at JavaScript's safe integer maximum; snapshots retain no identifiers,
payloads or credentials. Expired positions remain checkpoints and never invoke
`onMessage`.

Retained principal sends accept `ttl_seconds` (0–86400; zero disables delivery
expiry). Use `delivery: "retained"` for positive TTL. Send responses expose
`delivery_expires_at`; inbox history adds `ttl_seconds`, `delivery_expires_at`
and `expired`. Existing history `expires_at` remains the retention deadline.
Skip payload processing for REST rows marked `expired`, but advance the cursor.
Retries and edits retain the original deadline. Notification-fallback TTL remains
independent; set `notification_ttl_seconds` separately when needed.

Native inbox subscriptions ACK `inbox_checkpoint` expiry positions without calling
`onMessage`. Configure `inbox.onMessageExpired: expiry.record` using
`RealtimeExpiryDiagnostics`. Inbox observers receive `inbox: true`, consumer,
sequence, code and transport; no channel/principal/payload. Diagnostic snapshots
add `inboxCheckpoints` separately from channel WebSocket/SSE counts. Active inbox
messages expose optional `expiresAt`. Reconnects can repeat observations; hooks
are best effort and must not perform business effects. Upgrade server migration,
apid, realtime nodes and SDKs before opting into inbox TTL.
