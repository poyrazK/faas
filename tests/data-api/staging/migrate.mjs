import pg from 'pg'
import { readFileSync } from 'node:fs'

const version = readFileSync(new URL('./version', import.meta.url), 'utf8').trim()
if (!['1', '2'].includes(version) || !process.env.MIGRATION_DATABASE_URL) {
  throw new Error('canary_migration_configuration_invalid')
}
const client = new pg.Client({ connectionString: process.env.MIGRATION_DATABASE_URL })
try {
  await client.connect()
  await client.query('BEGIN')
  await client.query(`CREATE SCHEMA IF NOT EXISTS api;
    CREATE TABLE IF NOT EXISTS api.notes (
      id integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
      subject text NOT NULL, body text NOT NULL
    );
    ALTER TABLE api.notes ENABLE ROW LEVEL SECURITY;
    DROP POLICY IF EXISTS own_notes ON api.notes;
    CREATE POLICY own_notes ON api.notes
      USING (subject = current_setting('request.jwt.claims', true)::jsonb ->> 'sub')
      WITH CHECK (subject = current_setting('request.jwt.claims', true)::jsonb ->> 'sub');`)
  if (version === '2') {
    await client.query('ALTER TABLE api.notes ADD COLUMN IF NOT EXISTS priority integer NOT NULL DEFAULT 0')
  }
  await client.query('COMMIT')
} finally {
  await client.end()
}
