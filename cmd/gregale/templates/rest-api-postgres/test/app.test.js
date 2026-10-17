import test from "node:test";
import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import { createApp } from "../app.js";

function fixture(database) {
  const routes = new Map();
  const express = () => ({ use() {}, get(path, handler) { routes.set(`GET ${path}`, handler); }, post(path, handler) { routes.set(`POST ${path}`, handler); } });
  express.json = () => () => {};
  createApp(express, database);
  function request(route, { peer = "127.0.0.1", header = "1", body } = {}) {
    const req = Object.assign(new EventEmitter(), { socket: { remoteAddress: peer }, get: () => header, body });
    const res = Object.assign(new EventEmitter(), { statusCode: 200, status(code) { this.statusCode = code; return this; },
      json(body) { this.body = body; this.writableFinished = true; }, sendStatus(code) { this.statusCode = code; this.writableFinished = true; } });
    const done = routes.get(route)(req, res);
    return { req, res, done };
  }
  return request;
}

test("lifecycle hooks require the real loopback peer and exact platform header", async () => {
  const calls = { checkpoint: 0, restore: 0 };
  const request = fixture({ checkpoint: async () => { calls.checkpoint += 1; }, restore: async () => { calls.restore += 1; } });
  for (const route of ["POST /internal/before-checkpoint", "POST /internal/after-restore"]) {
    for (const options of [{ peer: "203.0.113.1" }, { peer: "127.0.0.2" }, { header: null }, { header: "0" }, { header: ["1", "1"] }, { header: "" }]) {
      const { res, done } = request(route, options);
      await done;
      assert.equal(res.statusCode, 403);
    }
    for (const peer of ["127.0.0.1", "::1", "::ffff:127.0.0.1"]) {
      const { res, done } = request(route, { peer }); await done; assert.equal(res.statusCode, 204);
    }
  }
  assert.deepEqual(calls, { checkpoint: 3, restore: 3 });
});

test("each hook checks its own marker header", async () => {
  const markers = [];
  const routes = new Map();
  const express = () => ({ use() {}, get() {}, post(path, handler) { routes.set(path, handler); } });
  express.json = () => () => {};
  createApp(express, { checkpoint: async () => {}, restore: async () => {} });
  for (const path of ["/internal/before-checkpoint", "/internal/after-restore"]) {
    const req = Object.assign(new EventEmitter(), { socket: { remoteAddress: "127.0.0.1" }, get: (name) => { markers.push(name); return "1"; } });
    const res = Object.assign(new EventEmitter(), { sendStatus() { this.writableFinished = true; } });
    await routes.get(path)(req, res);
  }
  assert.deepEqual(markers, ["X-Faas-Before-Checkpoint", "X-Faas-After-Restore"]);
});

test("HTTP SQL and restore failures do not disclose database errors", async () => {
  const error = new Error("postgres://user:secret@provider.example/db"); error.code = "secret";
  const request = fixture({ query: async () => { throw error; }, checkpoint: async () => { throw error; }, restore: async () => { throw error; } });
  for (const route of ["GET /healthz", "GET /notes", "POST /notes", "POST /internal/before-checkpoint", "POST /internal/after-restore"]) {
    const { res, done } = request(route, { body: { body: "note" } }); await done;
    assert.equal(res.statusCode, 503); assert.deepEqual(res.body, { ok: false, error: "database_unavailable" });
  }
});

test("disconnect aborts SQL work and request listeners are removed", async () => {
  const request = fixture({ query: async (_text, _values, { signal }) => new Promise((resolve) => signal.addEventListener("abort", resolve, { once: true })) });
  const { req, res, done } = request("GET /notes"); res.emit("close"); await done;
  assert.equal(req.listenerCount("aborted"), 0); assert.equal(res.listenerCount("close"), 0);
});

test("invalid note bodies never reach SQL and writes use explicit transactions", async () => {
  const calls = [];
  const request = fixture({ query: async (...args) => { calls.push(args); return { rows: [{ id: 1 }] }; } });
  for (const body of [undefined, { body: "" }, { body: {} }]) {
    const result = request("POST /notes", { body }); await result.done; assert.equal(result.res.statusCode, 400);
  }
  assert.equal(calls.length, 0);
  const result = request("POST /notes", { body: { body: "note" } }); await result.done;
  assert.equal(result.res.statusCode, 201); assert.equal(calls[0][2].write, true);
});
