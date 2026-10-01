import test from "node:test";
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { randomUUID } from "node:crypto";
import { createHandler } from "../app/http.js";

const alice = randomUUID(), bob = randomUUID();

async function fixture(t, store) {
  const server = createServer(createHandler(store));
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  t.after(() => new Promise((resolve) => { server.close(resolve); server.closeAllConnections(); }));
  return async (method, path, tenant, body, headers = {}) => {
    const response = await fetch(`http://127.0.0.1:${server.address().port}${path}`, { method,
      headers: { ...(tenant ? { "x-faas-platform-tenant-id": tenant } : {}), "content-type": "application/json", ...headers },
      body: body === undefined ? undefined : JSON.stringify(body) });
    return { status: response.status, body: await response.json(), cache: response.headers.get("cache-control") };
  };
}

function memoryStore() {
  const rows = new Map();
  const key = (tenant, id) => `${tenant}/${id}`;
  return {
    health: async () => {},
    list: async (tenant) => [...rows.entries()].filter(([k]) => k.startsWith(`${tenant}/`)).map(([, v]) => v),
    get: async (tenant, id) => rows.get(key(tenant, id)),
    create: async (tenant, data) => { const row = { id: randomUUID(), ...data }; rows.set(key(tenant, row.id), row); return row; },
    update: async (tenant, id, data) => { if (!rows.has(key(tenant, id))) return undefined;
      const row = { id, ...data }; rows.set(key(tenant, id), row); return row; },
    delete: async (tenant, id) => rows.delete(key(tenant, id)),
  };
}

test("two customers cannot read, update, delete, or select each other's documents", async (t) => {
  const call = await fixture(t, memoryStore());
  const created = await call("POST", "/documents", alice, { title: "Alice", content: "private" });
  assert.equal(created.status, 201);
  assert.equal(created.cache, "no-store");
  const path = `/documents/${created.body.id}`;
  assert.equal((await call("GET", path, alice)).body.content, "private");
  for (const method of ["GET", "PUT", "DELETE"]) {
    const body = method === "PUT" ? { title: "Bob", content: "replace" } : undefined;
    assert.equal((await call(method, path, bob, body)).status, 404, method);
  }
  assert.deepEqual((await call("GET", "/documents", bob)).body.documents, []);
  assert.equal((await call("GET", `/documents?tenant_id=${alice}`, bob)).status, 400);
  assert.equal((await call("POST", "/documents", bob, { title: "B", content: "", tenant_id: alice })).status, 400);
  assert.equal((await call("GET", path, alice)).body.content, "private");
  assert.equal((await call("PUT", path, alice, { title: "Updated", content: "new" })).status, 200);
  assert.equal((await call("DELETE", path, alice)).status, 200);
  assert.equal((await call("GET", path, alice)).status, 404);
});

test("only a single UUID tenant header admits document routes", async (t) => {
  let calls = 0;
  const call = await fixture(t, { health: async () => {}, list: async () => { calls++; return []; } });
  for (const tenant of [undefined, "customer-name", `${alice}, ${bob}`, "", "../../other"]) {
    assert.equal((await call("GET", "/documents", tenant)).status, 403);
  }
  assert.equal(calls, 0);
  assert.equal((await call("GET", "/healthz")).status, 200);
  assert.equal((await call("GET", "/documents", alice.toUpperCase())).status, 200);
});

test("invalid bodies and database failures produce bounded, secret-free responses", async (t) => {
  const store = memoryStore();
  store.list = async () => { throw new Error("postgres://secret:password@database/customer document"); };
  const call = await fixture(t, store);
  assert.deepEqual((await call("GET", "/documents", alice)).body, { error: "Service unavailable" });
  for (const body of [null, [], {}, { title: " ", content: "" }, { title: "x".repeat(201), content: "" }]) {
    assert.equal((await call("POST", "/documents", alice, body)).status, 400);
  }
  assert.equal((await call("POST", "/documents", alice, { title: "x", content: "x".repeat(21000) })).status, 413);
  assert.equal((await call("POST", "/documents", alice, {}, { "content-type": "text/plain" })).status, 415);
});
