import { readFile, realpath } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { poolConfig } from "./database.js";

export async function migrate(connectionString) {
  if (!connectionString) throw new Error("MIGRATION_DATABASE_URL is required");
  const { default: pg } = await import("pg");
  const client = new pg.Client(poolConfig(connectionString));
  // Active connection errors also reject the pending SQL operation. Handle
  // their event so the CLI can report its stable failure without a raw stack.
  client.on("error", () => {});
  try {
    await client.connect();
    await client.query("SET statement_timeout = '120s'");
    await client.query("SET lock_timeout = '30s'");
    await client.query("BEGIN");
    // Serialize schema changes across overlapping deployments.
    await client.query("SELECT pg_advisory_xact_lock(734928146)");
    await client.query(await readFile(new URL("schema.sql", import.meta.url), "utf8"));
    await client.query("COMMIT");
  } catch (error) {
    try { await client.query("ROLLBACK"); } catch { /* Connection may have failed. */ }
    throw error;
  } finally { await client.end(); }
}

if (process.argv[1] && await realpath(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try { await migrate(process.env.MIGRATION_DATABASE_URL); console.log("Notes schema ready"); }
  catch { console.error("Migration failed: check MIGRATION_DATABASE_URL, schema ownership, and lock contention"); process.exitCode = 1; }
}
