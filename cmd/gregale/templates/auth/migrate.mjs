import { readdir, readFile } from 'node:fs/promises'
import { pathToFileURL } from 'node:url'
import pg from 'pg'
import { migrationEnv, runtimeConfig } from './config.mjs'
import { applyGoTrueMigrations } from './upstream-migrate.mjs'

// Release task (ADR-1190/1545): apply GoTrue's verified files (with the Gregale
// overlay), then Gregale's gregale_auth migrations and the declarative
// AUTH_AUTHZ roles and policies, all through the auth binding, which acts as
// the auth schemas' owner. Serving instances never migrate, so snapshot
// restores perform no DDL.
export const sqlDirectory = new URL('./sql/', import.meta.url)

export async function applyGregaleMigrations(client, directory = sqlDirectory, authz = '{}') {
  const files = (await readdir(directory)).filter(name => /^\d{4}_[a-z0-9_]+\.sql$/.test(name)).sort()
  await client.query(`SELECT pg_advisory_lock(hashtextextended('gregale_auth_migrations', 0))`)
  try {
    await client.query('CREATE TABLE IF NOT EXISTS gregale_auth.schema_versions (version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())')
    const applied = new Set((await client.query('SELECT version FROM gregale_auth.schema_versions')).rows.map(row => row.version))
    for (const name of files) {
      if (applied.has(name)) continue
      await client.query('BEGIN')
      try {
        await client.query(await readFile(new URL(name, directory), 'utf8'))
        await client.query('INSERT INTO gregale_auth.schema_versions (version) VALUES ($1)', [name])
        await client.query('COMMIT')
      } catch (err) {
        await client.query('ROLLBACK')
        throw err
      }
    }
    await client.query('BEGIN')
    try {
      await client.query('SELECT gregale_auth.apply_authz($1::jsonb)', [authz])
      await client.query('COMMIT')
    } catch (err) {
      await client.query('ROLLBACK')
      throw err
    }
  } finally {
    await client.query(`SELECT pg_advisory_unlock(hashtextextended('gregale_auth_migrations', 0))`)
  }
}

function fail(event, err) {
  // SQLSTATE and stable messages only; never connection strings or SQL text.
  console.error(JSON.stringify({ level: 'error', event, sqlstate: err?.code ?? null, message: /^[a-z_]+$/.test(err?.message ?? '') ? err.message : undefined }))
  process.exit(1)
}

export async function main(env = process.env) {
  const config = runtimeConfig(env)
  const migrateEnv = migrationEnv(config)
  const client = new pg.Client({ connectionString: migrateEnv.DATABASE_URL })
  try {
    await client.connect()
    await applyGoTrueMigrations(client)
    await applyGregaleMigrations(client, sqlDirectory, config.authz)
  } catch (err) {
    fail('auth_migrate_failed', err)
  } finally {
    await client.end().catch(() => {})
  }
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) await main()
