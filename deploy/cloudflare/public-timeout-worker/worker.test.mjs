import assert from "node:assert/strict";
import test from "node:test";

import { handleRequest } from "./worker.mjs";

const request = () => new Request("https://demo.gregale.dev/delay", {
  headers: { "X-Faas-Request-Id": "req-worker-1" },
});

test("reconstructs a marked timeout response", async () => {
  const origin = new Response("error code: 504\n", {
    status: 504,
    headers: {
      "X-Faas-Error-Code": "request_budget_exceeded",
      "X-Faas-Request-Id": "req-worker-1",
      "Content-Type": "text/plain",
    },
  });
  const response = await handleRequest(request(), { ORIGIN_HOSTNAME: "origin.gregale.dev" }, async (_req, init) => {
    assert.equal(init.cf.resolveOverride, "origin.gregale.dev");
    return origin;
  });

  assert.equal(response.status, 504);
  assert.equal(response.headers.get("X-Faas-Error-Code"), "request_budget_exceeded");
  assert.equal(response.headers.get("X-Faas-Request-Id"), "req-worker-1");
  assert.equal(response.headers.get("Cache-Control"), "no-store");
  assert.match(await response.text(), /request_budget_exceeded/);
});

test("preserves an already structured timeout envelope", async () => {
  const response = await handleRequest(request(), { ORIGIN_HOSTNAME: "origin.gregale.dev" }, async () => new Response(
    JSON.stringify({ code: "request_budget_exceeded", status: 504 }),
    {
      status: 504,
      headers: {
        "X-Faas-Error-Code": "request_budget_exceeded",
        "Content-Type": "application/problem+json",
      },
    },
  ));

  assert.deepEqual(await response.json(), { code: "request_budget_exceeded", status: 504 });
});

test("does not rewrite genuine origin or CDN failures", async () => {
  const origin = new Response("origin timeout", {
    status: 504,
    headers: { "Content-Type": "text/plain" },
  });
  const response = await handleRequest(request(), { ORIGIN_HOSTNAME: "origin.gregale.dev" }, async () => origin);
  assert.equal(response, origin);
});

test("passes successful responses through unchanged", async () => {
  const origin = new Response("ok", { status: 200 });
  const response = await handleRequest(request(), { ORIGIN_HOSTNAME: "origin.gregale.dev" }, async () => origin);
  assert.equal(response, origin);
});

test("fails closed when the origin is not configured", async () => {
  const response = await handleRequest(request(), {}, async () => {
    throw new Error("must not fetch without origin config");
  });
  assert.equal(response.status, 500);
});
