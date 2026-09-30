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
import { consumeRealtimeChannel, RealtimeResyncRequiredError } from '@gregale/sdk-node';

try {
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
    webSocketFactory: async (url, protocols) => new WebSocket(url, protocols, {
      headers: { Authorization: `Bearer ${await getFreshOidcToken()}` },
    }),
  });
} catch (error) {
  if (error instanceof RealtimeResyncRequiredError) {
    // Rebuild application state, then save a new cursor before consuming again.
    console.log(error.oldestSequence, error.latestSequence);
  } else {
    throw error;
  }
}
```

The helper calls `onMessage`, saves its cursor, then sends the ack. If
processing or saving fails it stops without advancing. A crash between the
application side effect and cursor save can cause redelivery, so deduplicate
using the channel and sequence. When possible, store that deduplication key
with the application side effect in one transaction.
An expired cursor raises `RealtimeResyncRequiredError`; the helper never skips
missing history. Cancel with an `AbortSignal` to stop reconnecting. This preview
requires both server preview flags and the endpoint's channel authorization
callback described in [managed realtime operations](../../docs/ops/realtime.md).

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
