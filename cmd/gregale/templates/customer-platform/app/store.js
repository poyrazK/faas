import { randomUUID } from "node:crypto";

export async function openStore(connectionString) {
  if (!connectionString) throw new Error("DATABASE_URL is required");
  const { default: pg } = await import("pg");
  const pool = new pg.Pool({ connectionString, max: 4, connectionTimeoutMillis: 5000,
    idleTimeoutMillis: 10000, statement_timeout: 5000 });
  // Driver errors can include connection details. Report only a fixed message.
  pool.on("error", () => console.error("database connection unavailable"));
  const store = createStore(pool);
  try { await store.verify(); } catch (error) { await pool.end(); throw error; }
  return store;
}

export function createStore(pool) {
  async function inTenant(tenantID, operation) {
    const client = await pool.connect();
    let discard = false;
    try {
      await client.query("BEGIN");
      // Local to this transaction; never use a session-level SET on a pool.
      await client.query("SELECT set_config('gregale.platform_tenant_id', $1, true)", [tenantID]);
      const result = await operation(client);
      await client.query("COMMIT");
      return result;
    } catch (error) {
      try { await client.query("ROLLBACK"); } catch { discard = true; }
      throw error;
    } finally { client.release(discard); }
  }
  return {
    async verify() {
      const { rows: [role] } = await pool.query(
        "SELECT rolsuper, rolbypassrls FROM pg_roles WHERE rolname = current_user");
      if (!role || role.rolsuper || role.rolbypassrls) {
        throw new Error("DATABASE_URL must use a role without superuser or BYPASSRLS privileges");
      }
      const { rows: [table] } = await pool.query(
        "SELECT relrowsecurity, relforcerowsecurity FROM pg_class WHERE oid = to_regclass('public.customer_documents')");
      if (!table?.relrowsecurity || !table.relforcerowsecurity) {
        throw new Error("Run npm run migrate before starting; tenant row security must be enabled and forced");
      }
    },
    async health() { await pool.query("SELECT 1"); },
    list: (tenantID) => inTenant(tenantID, async (client) => {
      const { rows } = await client.query(
        "SELECT id, title, content, created_at FROM public.customer_documents WHERE tenant_id = $1 ORDER BY created_at DESC, id DESC LIMIT 100", [tenantID]);
      return rows;
    }),
    get: (tenantID, id) => inTenant(tenantID, async (client) => {
      const { rows } = await client.query(
        "SELECT id, title, content, created_at FROM public.customer_documents WHERE tenant_id = $1 AND id = $2", [tenantID, id]);
      return rows[0];
    }),
    create: (tenantID, document) => inTenant(tenantID, async (client) => {
      const { rows } = await client.query(
        "INSERT INTO public.customer_documents (tenant_id, id, title, content) VALUES ($1, $2, $3, $4) RETURNING id, title, content, created_at",
        [tenantID, randomUUID(), document.title, document.content]);
      return rows[0];
    }),
    update: (tenantID, id, document) => inTenant(tenantID, async (client) => {
      const { rows } = await client.query(
        "UPDATE public.customer_documents SET title = $3, content = $4 WHERE tenant_id = $1 AND id = $2 RETURNING id, title, content, created_at",
        [tenantID, id, document.title, document.content]);
      return rows[0];
    }),
    delete: (tenantID, id) => inTenant(tenantID, async (client) => {
      const { rowCount } = await client.query(
        "DELETE FROM public.customer_documents WHERE tenant_id = $1 AND id = $2", [tenantID, id]);
      return rowCount === 1;
    }),
    close: () => pool.end(),
  };
}
