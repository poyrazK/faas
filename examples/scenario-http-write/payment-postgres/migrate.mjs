import pg from "pg";
import { readFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";
import { poolConfig } from "./database.js";

export async function migrate(connectionString) {
  const client = new pg.Client(poolConfig(connectionString));
  client.on("error", () => {});
  try {
    await client.connect();
    await client.query("BEGIN");
    await client.query("SELECT pg_advisory_xact_lock(734928147)");
    await client.query(await readFile(new URL("schema.sql", import.meta.url), "utf8"));
    await client.query("COMMIT");
  } catch (error) {
    try { await client.query("ROLLBACK"); } catch { /* Connection may be gone. */ }
    throw error;
  } finally { await client.end(); }
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try { await migrate(process.env.MIGRATION_DATABASE_URL); console.log("Write ledger schema ready"); }
  catch { console.error("Write ledger migration failed"); process.exitCode = 1; }
}
