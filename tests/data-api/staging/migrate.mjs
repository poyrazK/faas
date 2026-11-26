import pg from 'pg'
import { existsSync, readFileSync } from 'node:fs'
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
        subject text NOT NULL, body text NOT NULL, tenant text NOT NULL DEFAULT 'canary'
      );
      ALTER TABLE api.notes ADD COLUMN IF NOT EXISTS exact_value bigint;
      ALTER TABLE api.notes ENABLE ROW LEVEL SECURITY;
      DROP POLICY IF EXISTS own_notes ON api.notes;
      CREATE POLICY own_notes ON api.notes
        USING (subject = current_setting('request.jwt.claims', true)::jsonb ->> 'sub' AND tenant = current_setting('request.jwt.claims', true)::jsonb ->> 'tenant')
        WITH CHECK (subject = current_setting('request.jwt.claims', true)::jsonb ->> 'sub' AND tenant = current_setting('request.jwt.claims', true)::jsonb ->> 'tenant');
      CREATE OR REPLACE FUNCTION api.create_note(note_body text) RETURNS SETOF api.notes
        LANGUAGE sql SECURITY INVOKER SET search_path=pg_catalog AS
        'INSERT INTO api.notes(subject,body) VALUES (current_setting(''request.jwt.claims'',true)::jsonb->>''sub'',note_body) RETURNING *';
      REVOKE EXECUTE ON FUNCTION api.create_note(text) FROM PUBLIC;
      COMMENT ON FUNCTION api.create_note(text) IS '@gregale:rpc';
      CREATE OR REPLACE FUNCTION api.private_note() RETURNS text
        LANGUAGE sql SECURITY INVOKER AS 'SELECT ''private''::text';
      REVOKE EXECUTE ON FUNCTION api.private_note() FROM PUBLIC;`)
    await client.query(readFileSync(new URL('./load-fixture.sql', import.meta.url), 'utf8'))
    if (existsSync(new URL('./connect-fixture', import.meta.url))) await client.query(`
      CREATE TABLE IF NOT EXISTS public.connect_canary_notes (id integer PRIMARY KEY, subject text NOT NULL);
      ALTER TABLE public.connect_canary_notes ENABLE ROW LEVEL SECURITY;
      DROP POLICY IF EXISTS connect_subject ON public.connect_canary_notes;
      CREATE POLICY connect_subject ON public.connect_canary_notes FOR SELECT USING (subject = current_setting('request.jwt.claims', true)::jsonb ->> 'sub');
      GRANT SELECT ON public.connect_canary_notes TO PUBLIC;
      INSERT INTO public.connect_canary_notes (id, subject) VALUES (1, 'alice'), (2, 'bob') ON CONFLICT (id) DO UPDATE SET subject=EXCLUDED.subject`)
    if (existsSync(new URL('./large-contract', import.meta.url))) {
      await client.query(`CREATE TABLE IF NOT EXISTS api.smoke_public_catalog (id integer PRIMARY KEY, name text NOT NULL);
        ALTER TABLE api.smoke_public_catalog ENABLE ROW LEVEL SECURITY;
        DROP POLICY IF EXISTS smoke_anonymous_read ON api.smoke_public_catalog;
        CREATE POLICY smoke_anonymous_read ON api.smoke_public_catalog FOR SELECT
          USING ((current_setting('request.jwt.claims', true)::jsonb ->> 'gregale_access') = 'anonymous');
        INSERT INTO api.smoke_public_catalog (id, name) VALUES (1, 'canary') ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name`)
      const columns = Array.from({ length: 70 }, (_, index) =>
        `"smoke_field_${String(index).padStart(3, '0')}${'_'.repeat(45)}" text`).join(', ')
      for (let index = 0; index < 80; index++) {
        const table = `smoke_large_${String(index).padStart(3, '0')}`
        await client.query(`CREATE TABLE IF NOT EXISTS api."${table}" (${columns})`)
        await client.query(`ALTER TABLE api."${table}" ENABLE ROW LEVEL SECURITY`)
      }
    }
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
