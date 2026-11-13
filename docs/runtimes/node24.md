# `node24` runtime

Node.js 24 LTS for functions. `node22` stays the default for new function
apps; choose `node24` as the app's runtime to use it. Existing `node22` apps
are unaffected. Builds use Railpack's Node plan, and the Node version comes
from the `node:24-trixie-slim` base image.

## Function contract

The [handler API](../handler-api.md) lists everything a handler receives
and can return.

Your source is a Node module exporting either `default async
function handler(event, ctx)` or a Fetch API object (`export default {
fetch(request, env, ctx) { ... } }`), served at `/app/node24.js`.
Your handler is loaded once, in a long-lived process that serves request
after request, so module-level state such as database clients is reused
between requests. Handlers written for the older stdin/stdout protocol still
run one process per request.
This is identical to the `node22` contract; the only differences are
the **handler filename** (`/app/node24.js` vs `/app/node22.js`) and
the runtime id (`node24` vs `node22`).

Fetch API handlers receive a standard WHATWG `Request` whose URL uses the
`http://faas.local` origin and whose path, query string, headers, and raw body
come from the incoming envelope. They return a standard `Response`; the
adapter preserves its status, headers, and bytes. The third `ctx` argument
includes `waitUntil()` for Workers-compatible handler code.

Gregale sets `FAAS_RUNTIME=node24` in the handler's environment
so your code can branch on the runtime.

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

A Fetch handler streams when its `Response` body is still being produced; a body that is already complete is sent as one response, as
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
up to 1 min on Hobby, 5 min on Pro and 15 min on Scale),
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

`node24` is a function runtime. To run your own Node HTTP server, deploy it
as an app instead: Gregale builds it from source (see [Builds](../build.md)) or
runs your container image (see [Apps](../apps.md)).

## Base image

Functions run on `node:24-trixie-slim` (Debian trixie). The base is shared
by every `node24` app, so only your `node_modules` and handler count toward
your plan's app layer limit (see [Plans and pricing](../plans.md)).

## Detection

Gregale checks a project for a Dockerfile first, then Node, Python and Go. A
Node project is recognized by `package.json`. The `engines.node` field does not pick
the version: set the app's runtime to `node22` or `node24`.

## Failure modes

| Symptom | Cause | Fix |
|---|---|---|
| `code: 'MODULE_NOT_FOUND'` when the handler runs | The handler imports an npm package that is not in `package.json` | Add it to `package.json`, commit the lockfile, redeploy |

## See also

- [Handler API](../handler-api.md)
- [Functions and runtimes](../functions.md)
- [Builds and framework detection](../build.md)
