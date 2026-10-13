import pg from "pg";
import { randomUUID } from "node:crypto";
import { conflict, fingerprint, normalizeOrder } from "./contract.js";
import { namespaceValue, poolConfig } from "./database.js";

export async function openPostgresLedger({ connectionString, namespace, tlsCA }) {
  namespaceValue(namespace);
  const pool = new pg.Pool({ ...poolConfig(connectionString), ...(tlsCA ? { ssl: { rejectUnauthorized: true, ca: tlsCA } } : {}), application_name: "gregale-write-ledger-" + namespace });
  pool.on("error", () => {});
  let id;
  let closing;
  try {
    await pool.query("INSERT INTO public.gregale_write_ledgers(namespace,id) VALUES($1,$2) ON CONFLICT(namespace) DO NOTHING", [namespace, randomUUID()]);
    const result = await pool.query("SELECT id FROM public.gregale_write_ledgers WHERE namespace=$1", [namespace]);
    id = result.rows[0].id;
  } catch (error) { await pool.end(); throw error; }
  return {
    id,
    namespace,
    poolStatus: () => ({ pool_total: pool.totalCount, pool_idle: pool.idleCount, pool_waiting: pool.waitingCount }),
    async get(key) {
      const result = await pool.query("SELECT state,fingerprint,payload AS \"order\" FROM public.gregale_write_charges WHERE namespace=$1 AND operation_key=$2", [namespace, key]);
      return result.rows[0];
    },
    async stats() {
      const result = await pool.query(`SELECT l.id AS ledger_id,
        count(*) FILTER (WHERE c.state='confirmed')::int AS charges,
        count(*) FILTER (WHERE c.state='pending')::int AS pending
        FROM public.gregale_write_ledgers l LEFT JOIN public.gregale_write_charges c USING(namespace)
        WHERE l.namespace=$1 GROUP BY l.id`, [namespace]);
      if (!result.rows[0] || result.rows[0].ledger_id !== id) throw new Error("ledger_identity_changed");
      return { ...result.rows[0], pool_total: pool.totalCount, pool_idle: pool.idleCount, pool_waiting: pool.waitingCount };
    },
    async bind(key, state, order) {
      if (!/^[a-zA-Z0-9_-]{1,128}$/.test(key) || !["pending", "confirmed"].includes(state)) throw new Error("invalid_ledger_entry");
      order = normalizeOrder(order);
      const hash = fingerprint(order);
      const client = await pool.connect();
      const lost = () => {};
      client.on("error", lost);
      let healthy = false;
      let commitSubmitted = false;
      try {
        await client.query("BEGIN");
        const inserted = await client.query(`INSERT INTO public.gregale_write_charges(namespace,operation_key,state,fingerprint,payload)
          VALUES($1,$2,$3,$4,$5) ON CONFLICT(namespace,operation_key) DO NOTHING`, [namespace, key, state, hash, order]);
        const result = await client.query(`SELECT state,fingerprint,payload AS "order" FROM public.gregale_write_charges
          WHERE namespace=$1 AND operation_key=$2 FOR UPDATE`, [namespace, key]);
        const record = result.rows[0];
        if (record.fingerprint !== hash) throw conflict();
        if (state === "confirmed" && record.state === "pending") {
          await client.query("UPDATE public.gregale_write_charges SET state='confirmed' WHERE namespace=$1 AND operation_key=$2", [namespace, key]);
          record.state = "confirmed";
        }
        commitSubmitted = true;
        await client.query("COMMIT");
        healthy = true;
        return { created: inserted.rowCount === 1, record };
      } catch (error) {
        const sqlState = typeof error.code === "string" && /^[0-9A-Z]{5}$/.test(error.code);
        const rejected = sqlState && error.severity === "ERROR" && !error.code.startsWith("08") && error.code !== "40003";
        if (commitSubmitted && !rejected) {
          // Destroy the uncertain connection. Never infer rollback from a lost
          // COMMIT reply and never automatically repeat the mutation.
          throw Object.assign(new Error("commit_outcome_unknown"), { outcomeUnknown: true });
        }
        try { await client.query("ROLLBACK"); healthy = true; } catch { /* Destroy uncertain connections. */ }
        throw error;
      } finally { client.off("error", lost); client.release(!healthy); }
    },
    close: () => closing ||= pool.end(),
  };
}
