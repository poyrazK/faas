# `node24` runtime

Node.js 24 LTS on the function surface (prewarmed persistent adapter with a
legacy per-request subprocess fallback, §4.9
envelope contract). Built by Railpack v0.31.1 with `--plan node`; the
underlying Node version is bound by the OCI base image
(`node:24-trixie-slim` from `images/runner-node24.Dockerfile`). No
new dispatch logic — Railpack's `--plan node` is version-agnostic, so
detection priority (`docker > node > python > go` in
`pkg/builderd/detect.go`) treats `node22` and `node24` identically.

`node24` is **additive on top of `node22`**: existing `node22` apps are
unaffected. There is no default-flip in this PR — `node22` stays the
default for new function apps. A future PR may flip once fleet-wide
`snapshot_fleet_avg_mb` is measured with both bases co-resident
(`pkg/api/limits.go::FleetSnapshotAvgTargetMB = 130`, alarm 160).

## Function contract

The customer's source is a Node module exporting either `default async
function handler(event, ctx)` or a Fetch API object (`export default {
fetch(request, env, ctx) { ... } }`), served at `/app/node24.js`.
Generated adapters are
prewarmed during runner startup and process newline-framed §4.9 envelopes in
one long-lived subprocess; legacy protocol handlers retain one subprocess
per request. The runner reads the envelope from stdin and the handler writes
the response envelope to stdout.
This is identical to the `node22` contract; the only differences are
the **handler filename** (`/app/node24.js` vs `/app/node22.js`) and
the runtime id (`node24` vs `node22`).

Fetch API handlers receive a standard WHATWG `Request` whose URL uses the
`http://faas.local` origin and whose path, query string, headers, and raw body
come from the incoming envelope. They return a standard `Response`; the
adapter preserves its status, headers, and bytes. The third `ctx` argument
includes `waitUntil()` for Workers-compatible handler code.

The runner shim sets `FAAS_RUNTIME=node24` in the handler's environment
so customers can branch on runtime if they want.

### Handler failures and retries

An uncaught handler exception is converted into a bounded HTTP 500 result with
`error: "handler_error"`, the exception message, and the invocation ID. It is a
terminal application failure for both synchronous and asynchronous invocation:
Gregale does not retry it on another instance or replay it after a rollback. The
persistent worker stays alive and can process the next invocation.

Wake, transport, and ordinary 5xx failures remain retryable under the durable
invocation policy. A retry resolves the deployment that is live at that later
attempt, so an infrastructure retry that spans a deploy or rollback can run on
the newly live revision. Returning a 4xx is terminal.

### Minimal handler

Write a Fetch API handler. It is the recommended shape: the same code runs
on Gregale, in tests with a plain `new Request(...)`, and on other
Fetch-style platforms.

```js
// handler.js
export default {
  async fetch(request, env, ctx) {
    const url = new URL(request.url);
    return Response.json({ hello: "world", path: url.pathname });
  },
};
```

`request` is a standard WHATWG `Request` (origin `http://faas.local`),
`env` is the process environment, and `ctx.signal` aborts when the client
goes away or the request runs out of time.

### Event handler

The older shape takes a parsed event and returns a plain object:

```js
// handler.js
export async function handler(event, ctx) {
  // event: { method, path, headers, query, body, body_b64 }
  // body is parsed JSON when the request was JSON, else a string.
  return {
    status: 200,                                  // or statusCode
    headers: { "content-type": "application/json" },
    body: { hello: "world" },                     // objects are sent as JSON
  };
}
```

A string or `Buffer` body is sent as is; any other value is JSON-encoded.
Returning an object without a `body` key sends the whole object as JSON.
`event.body_b64` holds the exact request bytes, for signature checks.

### Streaming

A Fetch handler streams when its `Response` body is still being produced
(ADR-999); a body that is already complete is sent as one response, as
before. An event handler streams by returning an async iterable `body`,
such as an async generator or a Node stream:

```js
// Fetch API: server-sent events
export default {
  async fetch() {
    const encoder = new TextEncoder();
    const body = new ReadableStream({
      async start(controller) {
        for (let i = 0; i < 3; i++) {
          controller.enqueue(encoder.encode(`data: tick ${i}\n\n`));
          await new Promise((resolve) => setTimeout(resolve, 1000));
        }
        controller.close();
      },
    });
    return new Response(body, { headers: { "content-type": "text/event-stream" } });
  },
};

// Event handler: an async generator body
export async function handler() {
  async function* tokens() { yield "hello "; yield "world"; }
  return { statusCode: 200, headers: { "content-type": "text/plain" }, body: tokens() };
}
```

An exception after streaming starts aborts the response; the client sees
a truncated body.
The whole invocation, the stream included, may run for the longer of the
app's request timeout and 30 s (`gregale app APP --request-timeout SECONDS`,
up to 1 min on Hobby, 5 min on Pro and 15 min on Scale; ADR-1150),
and the gateway carries it to the client on Hobby, Pro and Scale (Free
buffers it). Every `Set-Cookie` header is sent, and an event handler may
give any header a list of values.

### Testing locally

A Fetch API handler is a plain object, so a test calls it directly with
Node's built-in test runner and no Gregale tooling:

```js
// handler.test.js — run with `node --test`
import test from "node:test";
import assert from "node:assert/strict";
import handler from "./handler.js";

test("says hello", async () => {
  const res = await handler.fetch(new Request("http://faas.local/hello"), {}, {});
  assert.equal(res.status, 200);
  assert.deepEqual(await res.json(), { hello: "world", path: "/hello" });
});
```

To send a real request through the platform, use
`gregale invoke APP --path /hello`, or `gregale dev` for a live
edit-and-reload loop.

### CGO

CGO is not applicable to JS runtimes.

## App contract

`node24` is a function-only runtime in this PR — there is no App
deployment path. Customers wanting a Node HTTP server use `type: app`
and `image: registry.gregale.dev/<digest>` to deploy a regular OCI
image (no runner shim). Adding a Node app path is out of scope; if
the workaround becomes a common ask, file an issue and we'll design
the contract separately.

## Base image

- Base ref: `ghcr.io/onebox-faas/runner-node24:latest`
- Source: `images/runner-node24.Dockerfile`
  (`FROM node:24-trixie-slim`, digest-pinned in `images/Dockerfile.lock`)
- Disk: ~150 MB uncompressed, amortized across all `node24` apps via
  the two-drive scheme (drive0 = shared base, drive1 = per-app layer).
  Per-app cost is just the customer's `node_modules` + handler.

### Operational configuration

The runtime base is **auto-staged** by `imaged` through
`pkg/imaged/base_stage.go::EnsureRuntimeBase`. The deployment pipeline
must write `FAAS_DEPLOY_BASE_REF_NODE24` as an immutable OCI digest in
`/etc/faas/runtime-bases.env`; the default `:latest` is for unnamed
development daemons only.

The production workflow is:
1. Publish the `images/runner-node24.Dockerfile` image to
   `ghcr.io/onebox-faas/runner-node24` and record its config digest.
2. Let the deployment pipeline render that digest into
   `FAAS_DEPLOY_BASE_REF_NODE24`.
3. Start or restart `imaged`. It pulls, validates, and stages the ext4
   automatically; subsequent boots short-circuit on the digest sidecar.

Operators must not build, copy, or manually place a runtime `.ext4` on a
compute node. A missing or invalid base is a deployment error, not a reason
to fall back to a hand-staged artifact.

If a non-digest-pinned `FAAS_DEPLOY_BASE_REF_NODE24` is set (e.g.
`:latest`), imaged aborts startup loud with a one-line error naming
the offending env var. The retired global `FAAS_DEPLOY_BASE_REF` is rejected.

## Detection priority

`pkg/builderd/detect.go` priority order is
`docker > node > python > go`. The `node` branch matches on
`package.json` — it does NOT branch on the `engines.node` field, so a
tarball with `engines.node: ">=24"` still builds against the `node22`
or `node24` base depending on which runtime the customer chose
elsewhere. Version selection is operator-controlled via
`FAAS_DEPLOY_BASE_REF_NODE24`.

## Failure modes

| Symptom | Cause | Fix |
|---|---|---|
| `unsupported function runtime "node24"` | migration `00075` not applied, OR imaged built before PR 1 merge | `goose up`; rebuild + restart imaged |
| runner exec fails with `code: 'MODULE_NOT_FOUND'` | customer handler uses an npm dep not vendored into the tarball | bind via Railpack `package.json` lockfile, redeploy |
| `exec format error` on first wake | arm64 base deployed on amd64 host (or vice versa) | set `FAAS_DEPLOY_BASE_REF_NODE24` to the matching arch digest |

## See also

- `docs/STATUS.md` — runtime roster
- `pkg/api/build.go::FrameworkRailpackNode` — wire contract
- `pkg/builderd/dispatch.go::MapFramework` — detection → wire
- `guest/runners/node24/main.go` — function runner shim
- `guest/runners/node24/main_test.go::TestNode24RunnerHandlerDefault` —
  pins `/app/node24.js` against imaged argv
- `images/runner-node24.Dockerfile` — base image
- `migrations/00075_app_runtime_node24_python313.sql` — runtime enum widening

<!-- CI status: runtime migration, handler matrix, OCI auto-staging, and
runtime-image smoke coverage are implemented and enforced by CI. -->
