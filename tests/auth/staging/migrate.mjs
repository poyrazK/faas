import pg from 'pg'
import { pathToFileURL } from 'node:url'

// Row-level security that follows the Auth app: rows belong to the verified
// user, and stop being visible or writable once that user's session ends.
export async function migrate(connectionString) {
  if (!connectionString) throw new Error('canary_migration_configuration_invalid')
  const client = new pg.Client({ connectionString, connectionTimeoutMillis: 5000 })
  try {
    await client.connect()
    await client.query('BEGIN')
    await client.query("SET LOCAL statement_timeout='120s'; SET LOCAL lock_timeout='30s'")
    await client.query('SELECT pg_advisory_xact_lock(734928148)')
    await client.query(`CREATE SCHEMA IF NOT EXISTS api;
      CREATE TABLE IF NOT EXISTS api.notes (
        id integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
        owner uuid NOT NULL DEFAULT gregale_auth.uid(),
        body text NOT NULL
      );
      ALTER TABLE api.notes ENABLE ROW LEVEL SECURITY;
      DROP POLICY IF EXISTS own_notes ON api.notes;
      CREATE POLICY own_notes ON api.notes
        USING (owner = gregale_auth.uid() AND gregale_auth.session_active())
        WITH CHECK (owner = gregale_auth.uid() AND gregale_auth.session_active());`)
    await client.query('COMMIT')
  } catch (error) {
    try { await client.query('ROLLBACK') } catch {}
    throw error
  } finally { await client.end() }
}

if (process.argv[1] && pathToFileURL(process.argv[1]).href === import.meta.url) {
  try { await migrate(process.env.MIGRATION_DATABASE_URL) }
  catch { console.error('canary_migration_failed'); process.exitCode = 1 }
}
