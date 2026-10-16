import test from "node:test";
import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import { createApp } from "../app.js";

function fixture(database) {
  const routes = new Map();
  const express = () => ({ use() {}, get(path, handler) { routes.set(`GET ${path}`, handler); }, post(path, handler) { routes.set(`POST ${path}`, handler); } });
  express.json = () => () => {};
  createApp(express, database);
  function request(route, { peer = "127.0.0.1", header = "1", headers, body } = {}) {
    const marker = route.endsWith("before-checkpoint") ? "X-Faas-Before-Checkpoint" : "X-Faas-After-Restore";
    const received = headers ?? { [marker]: header };
    const req = Object.assign(new EventEmitter(), { socket: { remoteAddress: peer }, get: (name) => received[name], body });
    const res = Object.assign(new EventEmitter(), { statusCode: 200, status(code) { this.statusCode = code; return this; },
      json(body) { this.body = body; this.writableFinished = true; }, sendStatus(code) { this.statusCode = code; this.writableFinished = true; } });
    const done = routes.get(route)(req, res);
    return { req, res, done };
  }
  return request;
}

for (const route of ["POST /internal/after-restore", "POST /internal/before-checkpoint"]) {
test(`${route} requires the real loopback peer and exact platform header`, async () => {
  let restored = 0;
  const apply = async () => { restored += 1; };
  const request = fixture({ restore: apply, beforeCheckpoint: apply });
  const wrongMarker = route.endsWith("before-checkpoint") ? "X-Faas-After-Restore" : "X-Faas-Before-Checkpoint";
  for (const options of [{ peer: "203.0.113.1" }, { peer: "127.0.0.2" }, { header: null }, { header: "0" }, { header: ["1", "1"] }, { headers: { [wrongMarker]: "1" } }]) {
    const { res, done } = request(route, options);
    await done;
    assert.equal(res.statusCode, 403);
  }
  const denied = request(route, { header: "" }); await denied.done; assert.equal(denied.res.statusCode, 403);
  for (const peer of ["127.0.0.1", "::1", "::ffff:127.0.0.1"]) {
    const { res, done } = request(route, { peer }); await done; assert.equal(res.statusCode, 204);
  }
  assert.equal(restored, 3);
});
}

test("HTTP SQL and restore failures do not disclose database errors", async () => {
  const error = new Error("postgres://user:secret@provider.example/db"); error.code = "secret";
  const request = fixture({ query: async () => { throw error; }, restore: async () => { throw error; }, beforeCheckpoint: async () => { throw error; } });
  for (const route of ["GET /healthz", "GET /notes", "POST /notes", "POST /internal/after-restore", "POST /internal/before-checkpoint"]) {
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
