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

`consumeRealtimeChannel` processes one v2 channel and reconnects with the last
saved cursor. Supply a durable cursor store and a WebSocket factory that adds
the endpoint's OIDC bearer token. For example, with the separate `ws` package
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

The helper calls `onMessage`, saves its cursor, then sends the ack. If
processing or saving fails it stops without advancing. A crash between the
application side effect and cursor save can cause redelivery, so deduplicate
using the channel and sequence. When possible, store that deduplication key
with the application side effect in one transaction.
An expired cursor calls `onResync` with the channel, stale cursor, and retained
history bounds. Return a cursor between `oldestSequence - 1` and
`latestSequence` that the rebuilt state fully represents; the helper saves it
before reconnecting. Omit `onResync` to receive `RealtimeResyncRequiredError`
and perform recovery outside the consumer. Cancel with an `AbortSignal` to
stop reconnecting. This preview requires both server preview flags and the
endpoint's channel authorization callback described in
[managed realtime operations](../../docs/ops/realtime.md).

Use `consumeRealtimeChannels` to multiplex up to eight channels on one
WebSocket. Each channel has its own cursor store, message handler, and optional
resync callback; the connection URL, token factory, retry policy, and abort
signal are shared:

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
backpressure to every channel on that socket. A resync on one channel saves
that channel's rebuilt cursor and reconnects the shared socket; the other
channels resume from their own saved cursors and can receive duplicates under
the at-least-once delivery model. `consumeRealtimeChannel` remains available
for a single channel and uses the same implementation.

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
