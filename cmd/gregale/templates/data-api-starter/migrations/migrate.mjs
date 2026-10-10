import { createHash } from 'node:crypto'
import { readdir, readFile, realpath } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

export async function migrationFiles(directory) {
  const names = (await readdir(directory)).sort()
  if (!names.length || names.some(name => !/^\d{4}_[a-z0-9_]+\.sql$/.test(name))) {
    throw new Error('Use numbered SQL files such as 0001_notes.sql')
  }
  const versions = new Set()
  return Promise.all(names.map(async name => {
    const version = Number(name.slice(0, 4))
    if (version === 0 || versions.has(version)) throw new Error('Migration versions must be positive and unique')
    versions.add(version)
    const sql = await readFile(new URL(name, directory), 'utf8')
    return { version, name, sql, checksum: createHash('sha256').update(sql).digest('hex') }
  }))
}

export async function migrate(connectionString, directory = new URL('./sql/', import.meta.url)) {
  if (!connectionString) throw new Error('MIGRATION_DATABASE_URL is required')
  const files = await migrationFiles(directory)
  const { default: pg } = await import('pg')
  const client = new pg.Client({ connectionString, connectionTimeoutMillis: 5000 })
  try {
    await client.connect()
    await client.query('BEGIN')
    await client.query("SET LOCAL statement_timeout = '120s'")
    await client.query("SET LOCAL lock_timeout = '30s'")
    await client.query('SELECT pg_advisory_xact_lock(734928147)')
    // Keep the ledger outside the exposed api schema and its default grants.
    await client.query(`CREATE SCHEMA IF NOT EXISTS gregale_migrations;
      REVOKE ALL ON SCHEMA gregale_migrations FROM PUBLIC;
      CREATE TABLE IF NOT EXISTS gregale_migrations.applied (
        version integer PRIMARY KEY, name text NOT NULL, checksum text NOT NULL,
        applied_at timestamptz NOT NULL DEFAULT now()
      );`)
    const { rows } = await client.query('SELECT version, name, checksum FROM gregale_migrations.applied ORDER BY version')
    const applied = new Map(rows.map(row => [row.version, row]))
    for (const row of rows) {
      const file = files.find(file => file.version === row.version)
      if (!file || file.name !== row.name || file.checksum !== row.checksum) {
        throw new Error('An applied migration was changed or removed; add a new migration instead')
      }
    }
    const latest = rows.at(-1)?.version || 0
    for (const file of files) {
      if (applied.has(file.version)) continue
      if (file.version < latest) throw new Error('New migrations must follow the applied versions')
      await client.query(file.sql)
      await client.query('INSERT INTO gregale_migrations.applied(version, name, checksum) VALUES ($1, $2, $3)', [file.version, file.name, file.checksum])
    }
    await client.query('COMMIT')
  } catch (error) {
    try { await client.query('ROLLBACK') } catch { /* A connection failure can prevent rollback. */ }
    throw error
  } finally {
    await client.end()
  }
}

if (process.argv[1] && await realpath(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    await migrate(process.env.MIGRATION_DATABASE_URL)
    console.log('Migrations complete')
  } catch {
    // Provider errors can contain a connection string. Keep CLI output bounded
    // to a stable diagnostic rather than printing the driver error.
    console.error('Migration failed: check the migration binding, SQL, migration history and lock contention')
    process.exitCode = 1
  }
}
