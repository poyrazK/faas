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

test("recovers a canonical timeout when a proxy strips the marker", async () => {
  const response = await handleRequest(request(), { ORIGIN_HOSTNAME: "origin.gregale.dev" }, async () => new Response(
    JSON.stringify({ code: "request_budget_exceeded", status: 504 }),
    {
      status: 504,
      headers: { "Content-Type": "application/problem+json" },
    },
  ));

  assert.equal(response.status, 504);
  assert.equal(response.headers.get("X-Faas-Error-Code"), "request_budget_exceeded");
  assert.equal(response.headers.get("Cache-Control"), "no-store");
  assert.deepEqual(await response.json(), { code: "request_budget_exceeded", status: 504 });
});

test("decodes a budget timeout transported around Cloudflare's origin error page", async () => {
  const response = await handleRequest(request(), { ORIGIN_HOSTNAME: "origin.gregale.dev" }, async () => new Response(
    JSON.stringify({ code: "request_budget_exceeded", status: 504 }),
    {
      status: 409,
      headers: {
        "X-Faas-Edge-Original-Status": "504",
        "X-Faas-Error-Code": "request_budget_exceeded",
        "Content-Type": "application/problem+json",
      },
    },
  ));

  assert.equal(response.status, 504);
  assert.equal(response.headers.get("X-Faas-Edge-Original-Status"), null);
  assert.equal(response.headers.get("X-Faas-Error-Code"), "request_budget_exceeded");
  assert.deepEqual(await response.json(), { code: "request_budget_exceeded", status: 504 });
});

test("decodes an application's unmarked 504 without reclassifying it", async () => {
  const response = await handleRequest(request(), { ORIGIN_HOSTNAME: "origin.gregale.dev" }, async () => new Response(
    "application timeout",
    {
      status: 409,
      headers: {
        "X-Faas-Edge-Original-Status": "504",
        "Content-Type": "text/plain",
      },
    },
  ));

  assert.equal(response.status, 504);
  assert.equal(response.headers.get("X-Faas-Edge-Original-Status"), null);
  assert.equal(response.headers.get("X-Faas-Error-Code"), null);
  assert.equal(await response.text(), "application timeout");
});

test("advertises the statuses it can restore", async () => {
  await handleRequest(request(), { ORIGIN_HOSTNAME: "origin.gregale.dev" }, async (req) => {
    assert.equal(req.headers.get("X-Faas-Edge-Restorable"), "502,504");
    assert.equal(req.headers.get("X-Faas-Request-Id"), "req-worker-1");
    return new Response("ok", { status: 200 });
  });
});

// Production-us hunt #9 (H9-96): an app OOM-killed mid-request produced the
// gateway's "App connection failed" problem, but Cloudflare replaced the
// origin 502 with its HTML error page, dropping the JSON body and request id.
test("restores a transported 502 with its problem body", async () => {
  const response = await handleRequest(request(), { ORIGIN_HOSTNAME: "origin.gregale.dev" }, async () => new Response(
    JSON.stringify({ code: "bad_gateway", status: 502 }),
    {
      status: 409,
      headers: {
        "X-Faas-Edge-Original-Status": "502",
        "X-Faas-Request-Id": "req-worker-1",
        "Content-Type": "application/problem+json",
      },
    },
  ));

  assert.equal(response.status, 502);
  assert.equal(response.headers.get("X-Faas-Edge-Original-Status"), null);
  assert.equal(response.headers.get("X-Faas-Request-Id"), "req-worker-1");
  assert.equal(response.headers.get("Content-Type"), "application/problem+json");
  assert.deepEqual(await response.json(), { code: "bad_gateway", status: 502 });
});

test("does not rewrite genuine origin or CDN failures", async () => {
  const origin = new Response("origin timeout", {
    status: 504,
    headers: { "Content-Type": "text/plain" },
  });
  const response = await handleRequest(request(), { ORIGIN_HOSTNAME: "origin.gregale.dev" }, async () => origin);
  assert.notEqual(response, origin);
  assert.equal(response.status, 504);
  assert.equal(response.headers.get("Content-Type"), "text/plain");
  assert.equal(await response.text(), "origin timeout");
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

for (const path of ["/", "/dashboard", "/dashboard/apps/example"]) {
  test(`hardens frontend HTML route ${path}`, async () => {
    const request = new Request(`https://gregale.dev${path}`);
    const response = await handleRequest(request, {}, async () => new Response("<html></html>", {
      status: 200,
      headers: {
        "Content-Type": "text/html; charset=utf-8",
        "Access-Control-Allow-Origin": "*",
      },
    }));

    assert.equal(response.status, 200);
    assert.match(response.headers.get("Content-Security-Policy"), /frame-ancestors 'none'/);
    assert.match(response.headers.get("Content-Security-Policy"), /connect-src 'self' https:\/\/api\.gregale\.dev wss:\/\/api\.gregale\.dev/);
    assert.equal(response.headers.get("X-Content-Type-Options"), "nosniff");
    assert.equal(response.headers.get("Referrer-Policy"), "strict-origin-when-cross-origin");
    assert.equal(response.headers.get("Permissions-Policy"), "camera=(), microphone=(), geolocation=(), payment=(), usb=()");
    assert.equal(response.headers.get("X-Frame-Options"), "DENY");
    assert.equal(response.headers.get("Access-Control-Allow-Origin"), null);
  });
}

test("does not impose document headers on frontend assets", async () => {
  const origin = new Response("export default {}", {
    status: 200,
    headers: { "Content-Type": "text/javascript", "Access-Control-Allow-Origin": "*" },
  });
  const response = await handleRequest(new Request("https://gregale.dev/app.js"), {}, async () => origin);
  assert.equal(response, origin);
  assert.equal(response.headers.get("Content-Security-Policy"), null);
  assert.equal(response.headers.get("Access-Control-Allow-Origin"), "*");
});

// ADR-1343 pilot: listed app hosts resolve to the compute-node app edge's
// tunnel; every other host keeps the control-plane origin.
const appEdgeEnv = {
  ORIGIN_HOSTNAME: "origin.gregale.dev",
  APP_EDGE_ORIGIN_HOSTNAME: "app-edge.gregale.dev",
  APP_EDGE_HOSTS: " Pilot.gregale.dev , other-pilot.gregale.dev",
};

for (const [name, url, env, want] of [
  ["listed pilot host goes to the app edge", "https://pilot.gregale.dev/", appEdgeEnv, "app-edge.gregale.dev"],
  ["second listed host goes to the app edge", "https://other-pilot.gregale.dev/x", appEdgeEnv, "app-edge.gregale.dev"],
  ["unlisted app host stays on the control plane", "https://demo.gregale.dev/", appEdgeEnv, "origin.gregale.dev"],
  ["platform host stays on the control plane", "https://api.gregale.dev/v1/apps", appEdgeEnv, "origin.gregale.dev"],
  ["no app edge origin keeps every host on the control plane", "https://pilot.gregale.dev/",
    { ORIGIN_HOSTNAME: "origin.gregale.dev", APP_EDGE_HOSTS: "pilot.gregale.dev" }, "origin.gregale.dev"],
  ["no listed hosts keeps every host on the control plane", "https://pilot.gregale.dev/",
    { ORIGIN_HOSTNAME: "origin.gregale.dev", APP_EDGE_ORIGIN_HOSTNAME: "app-edge.gregale.dev" }, "origin.gregale.dev"],
]) {
  test(`origin choice: ${name}`, async () => {
    let resolved;
    const response = await handleRequest(new Request(url), env, async (req, init) => {
      resolved = init.cf.resolveOverride;
      assert.equal(new URL(req.url).hostname, new URL(url).hostname, "the customer Host must survive the override");
      return new Response("ok", { status: 200 });
    });
    assert.equal(response.status, 200);
    assert.equal(resolved, want);
  });
}

test("a pilot host still restores a transported 504 from the app edge", async () => {
  const origin = new Response(JSON.stringify({ code: "request_budget_exceeded", status: 504 }), {
    status: 409,
    headers: {
      "X-Faas-Edge-Original-Status": "504",
      "X-Faas-Error-Code": "request_budget_exceeded",
      "Content-Type": "application/problem+json",
    },
  });
  const response = await handleRequest(new Request("https://pilot.gregale.dev/slow"), appEdgeEnv, async (_req, init) => {
    assert.equal(init.cf.resolveOverride, "app-edge.gregale.dev");
    return origin;
  });
  assert.equal(response.status, 504);
  assert.equal(response.headers.get("X-Faas-Edge-Original-Status"), null);
  assert.equal(response.headers.get("X-Faas-Error-Code"), "request_budget_exceeded");
});

// ADR-1343 cutover: "*" moves every app host and nothing else.
const allAppsEnv = {
  ORIGIN_HOSTNAME: "origin.gregale.dev",
  APP_EDGE_ORIGIN_HOSTNAME: "app-edge.gregale.dev",
  APP_EDGE_HOSTS: "*",
  APP_EDGE_EXCLUDE_HOSTS: "kept-on-cp.gregale.dev",
};

for (const [host, want] of [
  ["shop.gregale.dev", "app-edge.gregale.dev"],
  ["pr-7-shop.gregale.dev", "app-edge.gregale.dev"],
  ["a1b.gregale.dev", "app-edge.gregale.dev"],
  ["api.gregale.dev", "origin.gregale.dev"],
  ["operations.gregale.dev", "origin.gregale.dev"],
  ["s3.gregale.dev", "origin.gregale.dev"],
  ["app-edge.gregale.dev", "origin.gregale.dev"],
  ["origin.gregale.dev", "origin.gregale.dev"],
  ["status.gregale.dev", "origin.gregale.dev"],
  ["www.gregale.dev", "origin.gregale.dev"],
  ["tag-shop.gregale.dev", "origin.gregale.dev"],
  ["ab.gregale.dev", "origin.gregale.dev"],
  ["deep.shop.gregale.dev", "origin.gregale.dev"],
  ["shop.example.com", "origin.gregale.dev"],
  ["kept-on-cp.gregale.dev", "origin.gregale.dev"],
]) {
  test(`all-apps mode: ${host} -> ${want}`, async () => {
    let resolved;
    await handleRequest(new Request(`https://${host}/`), allAppsEnv, async (_req, init) => {
      resolved = init.cf.resolveOverride;
      return new Response("ok");
    });
    assert.equal(resolved, want);
  });
}

test("all-apps mode follows APPS_DOMAIN", async () => {
  let resolved;
  await handleRequest(
    new Request("https://shop.apps.example.net/"),
    { ...allAppsEnv, APPS_DOMAIN: "apps.example.net" },
    async (_req, init) => {
      resolved = init.cf.resolveOverride;
      return new Response("ok");
    },
  );
  assert.equal(resolved, "app-edge.gregale.dev");
});

test("an excluded host stays on the control plane even when listed", async () => {
  let resolved;
  await handleRequest(
    new Request("https://pilot.gregale.dev/"),
    { ...appEdgeEnv, APP_EDGE_EXCLUDE_HOSTS: "pilot.gregale.dev" },
    async (_req, init) => {
      resolved = init.cf.resolveOverride;
      return new Response("ok");
    },
  );
  assert.equal(resolved, "origin.gregale.dev");
});
