// Schema creation against a recording client: the first slice of a new
// database creates the schema under one advisory lock, so slices that start
// together on an empty database do not collide in Postgres's catalog.
import assert from "node:assert/strict";
import { test } from "node:test";
import { PostgresStore, schema, schemaLock } from "../store.js";

function recordingStore() {
  const queries = [];
  const store = new PostgresStore("postgres://agent.test/db", {
    newClient: () => ({
      connect: async () => {},
      end: async () => {},
      query: async (text) => { queries.push(text); return { rows: [], rowCount: 0 }; },
    }),
  });
  return { store, queries };
}

test("the schema takes the advisory lock before its first statement, in one query", () => {
  const statements = schema.split(";").map((s) => s.trim()).filter(Boolean);
  assert.equal(statements[0], `SELECT pg_advisory_xact_lock(${schemaLock})`);
  assert.ok(statements.slice(1).every((s) => s.startsWith("CREATE ")), statements.join("\n"));
});

test("a store sends the locked schema once, before the slice's own queries", async () => {
  const { store, queries } = recordingStore();
  await store.thread("t-1");
  await store.thread("t-1");
  assert.equal(queries.filter((q) => q === schema).length, 1);
  assert.equal(queries[0], schema);
  assert.match(queries[1], /FROM agent_messages/);
});
