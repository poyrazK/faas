import { readFile } from "node:fs/promises";

export async function migrate(connectionString) {
  if (!connectionString) throw new Error("DATABASE_URL is required");
  const { default: pg } = await import("pg");
  const client = new pg.Client({ connectionString, connectionTimeoutMillis: 5000 });
  try {
    await client.connect();
    await client.query("BEGIN");
    // Serializes repeated release attempts across deployments.
    await client.query("SELECT pg_advisory_xact_lock(734928145)");
    await client.query(await readFile(new URL("schema.sql", import.meta.url), "utf8"));
    await client.query("COMMIT");
  } finally { await client.end(); }
}

if (process.argv[1] && import.meta.url === (await import("node:url")).pathToFileURL(process.argv[1]).href) {
  try { await migrate(process.env.DATABASE_URL); console.log("Customer document schema ready"); }
  catch { console.error("Migration failed: check DATABASE_URL and schema ownership"); process.exitCode = 1; }
}
