import test from "node:test";
import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { migrate } from "../app/migrate.js";
import { createStore } from "../app/store.js";

// Explicit gate fails if not configured; npm test has no external dependencies.
test("PostgreSQL tenant isolation, rollback and connection reuse", async (t) => {
  assert.ok(process.env.CUSTOMER_DATABASE_URL, "Set CUSTOMER_DATABASE_URL to a disposable non-superuser database");
  const { default: pg } = await import("pg");
  assert.ok(process.env.CUSTOMER_MIGRATION_DATABASE_URL, "Set CUSTOMER_MIGRATION_DATABASE_URL to the schema owner connection");
  await migrate(process.env.CUSTOMER_MIGRATION_DATABASE_URL);
  const pool = new pg.Pool({ connectionString: process.env.CUSTOMER_DATABASE_URL, max: 1 });
  const store = createStore(pool), alice = randomUUID(), bob = randomUUID();
  let doc;
  t.after(async () => { try { if (doc) await store.delete(alice, doc.id); } finally { await pool.end(); } });
  await store.verify();
  const { rows: [ownership] } = await pool.query(
    "SELECT current_user AS runtime_role, tableowner FROM pg_tables WHERE schemaname = 'public' AND tablename = 'customer_documents'");
  assert.notEqual(ownership.runtime_role, ownership.tableowner, "runtime must not own the application table");
  await assert.rejects(pool.query("CREATE TABLE public.runtime_ddl_probe (id integer)"), (error) => error.code === "42501");
  await assert.rejects(pool.query("ALTER TABLE public.customer_documents DISABLE ROW LEVEL SECURITY"), (error) => error.code === "42501");
  doc = await store.create(alice, { title: "Alice", content: "private" });
  assert.equal(await store.get(bob, doc.id), undefined);
  assert.equal(await store.update(bob, doc.id, { title: "Bob", content: "stolen" }), undefined);
  assert.equal(await store.delete(bob, doc.id), false);
  assert.deepEqual(await store.list(bob), []);
  // Even deliberately unscoped SQL is constrained by the database policy.
  assert.deepEqual((await pool.query("SELECT id FROM public.customer_documents")).rows, []);
  const client = await pool.connect();
  try {
    await client.query("BEGIN");
    await client.query("SELECT set_config('gregale.platform_tenant_id', $1, true)", [bob]);
    assert.equal((await client.query("SELECT id FROM public.customer_documents WHERE id = $1", [doc.id])).rowCount, 0);
    assert.equal((await client.query("UPDATE public.customer_documents SET title = 'wrong' WHERE id = $1", [doc.id])).rowCount, 0);
    await assert.rejects(client.query("INSERT INTO public.customer_documents (tenant_id, id, title, content) VALUES ($1, $2, 'wrong', '')", [alice, randomUUID()]), (error) => error.code === "42501");
  } finally { await client.query("ROLLBACK"); client.release(); }
  await assert.rejects(store.create(bob, { title: "", content: "" }));
  assert.equal((await store.get(alice, doc.id)).content, "private");
  assert.deepEqual((await pool.query("SELECT id FROM public.customer_documents")).rows, []);
});

test("startup refuses roles that bypass row security", async () => {
  const pool = { query: async () => ({ rows: [{ rolsuper: true, rolbypassrls: true }] }) };
  await assert.rejects(createStore(pool).verify(), /without superuser or BYPASSRLS/);
});
