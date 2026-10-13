import pg from "../payment-postgres/client.js";
import { pathToFileURL } from "node:url";
import { namespaceValue, poolConfig } from "../payment-postgres/database.js";

export async function cleanupNamespace(connectionString, namespace, checkOnly = false) {
  namespaceValue(namespace);
  const pool = new pg.Pool(poolConfig(connectionString));
  pool.on("error", () => {});
  try {
    if (!checkOnly) await pool.query("DELETE FROM public.gregale_write_ledgers WHERE namespace=$1", [namespace]);
    const result = await pool.query(`SELECT (SELECT count(*) FROM public.gregale_write_ledgers WHERE namespace=$1)
      + (SELECT count(*) FROM public.gregale_write_charges WHERE namespace=$1) AS remaining`, [namespace]);
    if (Number(result.rows[0].remaining) !== 0) throw new Error("ledger_cleanup_incomplete");
    return { namespace, passed: true, remaining: 0 };
  } finally { await pool.end(); }
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    if (process.env.GREGALE_DEPENDENCY_DATABASE_DISPOSABLE !== "1") throw new Error("disposable_database_required");
    const namespace = process.argv[3] || process.env.GREGALE_TEST_RUN_ID;
    console.log(JSON.stringify(await cleanupNamespace(process.env.GREGALE_DEPENDENCY_DATABASE_URL, namespace, process.argv[2] === "check")));
  } catch { console.error("Postgres ledger cleanup verification failed"); process.exitCode = 1; }
}
