import pg from 'pg'
import { readFileSync } from 'node:fs'
import { pathToFileURL } from 'node:url'

export async function migrate(connectionString, version = readFileSync(new URL('./version', import.meta.url), 'utf8').trim()) {
  if (!['1', '2'].includes(version) || !connectionString) throw new Error('canary_migration_configuration_invalid')
  const client = new pg.Client({ connectionString, connectionTimeoutMillis: 5000 })
  try {
    await client.connect()
    await client.query('BEGIN')
    await client.query("SET LOCAL statement_timeout='120s'; SET LOCAL lock_timeout='30s'")
    await client.query('SELECT pg_advisory_xact_lock(734928147)')
    await client.query(`CREATE SCHEMA IF NOT EXISTS api;
      CREATE TABLE IF NOT EXISTS api.notes (
        id integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
        subject text NOT NULL, body text NOT NULL
      );
      ALTER TABLE api.notes ENABLE ROW LEVEL SECURITY;
      DROP POLICY IF EXISTS own_notes ON api.notes;
      CREATE POLICY own_notes ON api.notes
        USING (subject = current_setting('request.jwt.claims', true)::jsonb ->> 'sub')
        WITH CHECK (subject = current_setting('request.jwt.claims', true)::jsonb ->> 'sub');
      CREATE OR REPLACE FUNCTION api.create_note(note_body text) RETURNS SETOF api.notes
        LANGUAGE sql SECURITY INVOKER SET search_path=pg_catalog AS
        'INSERT INTO api.notes(subject,body) VALUES (current_setting(''request.jwt.claims'',true)::jsonb->>''sub'',note_body) RETURNING *';
      REVOKE EXECUTE ON FUNCTION api.create_note(text) FROM PUBLIC;
      COMMENT ON FUNCTION api.create_note(text) IS '@gregale:rpc';
      CREATE OR REPLACE FUNCTION api.private_note() RETURNS text
        LANGUAGE sql SECURITY INVOKER AS 'SELECT ''private''::text';
      REVOKE EXECUTE ON FUNCTION api.private_note() FROM PUBLIC;`)
    if (version === '2') await client.query('ALTER TABLE api.notes ADD COLUMN IF NOT EXISTS priority integer NOT NULL DEFAULT 0')
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
