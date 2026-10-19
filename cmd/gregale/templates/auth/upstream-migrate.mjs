// ADR-1711: GoTrue's migrate command uses embedded SQL, not the image's overlay.
import { createHash } from 'node:crypto'
import { readFile, readdir } from 'node:fs/promises'
import { pathToFileURL } from 'node:url'
import { gotrue } from './config.mjs'

const directoryURL = pathToFileURL(gotrue.migrations + '/')
const manifestURL = new URL('./migrations.sha256', import.meta.url)
const overlayURL = new URL('./overlay/', import.meta.url)

function namespaceSQL(source) {
  // These are the only template actions in the 75 digest-pinned migrations.
  const sql = source.replace(/\{\{\s*index\s+\.Options\s+"Namespace"\s*\}\}/g, 'auth')
  if (sql.includes('{{') || sql.includes('}}')) throw new Error('unsupported_gotrue_migration_template')
  return sql
}

export async function verifiedGoTrueMigrations(directory = directoryURL, manifest = manifestURL, overlays = overlayURL) {
  const expected = new Map(), versions = new Set()
  for (const line of (await readFile(manifest, 'utf8')).trimEnd().split('\n')) {
    const match = /^([a-f0-9]{64})  ((\d{2,14})_[a-z0-9_]+(?:\.postgres)?\.up\.sql)$/.exec(line)
    if (!match || expected.has(match[2]) || versions.has(match[3])) throw new Error('invalid_gotrue_migration_manifest')
    expected.set(match[2], { digest: match[1], version: match[3] }); versions.add(match[3])
  }
  const entries = await readdir(directory, { withFileTypes: true })
  if (!expected.size || entries.length !== expected.size || entries.some(entry => !entry.isFile() || !expected.has(entry.name))) throw new Error('gotrue_migration_files_changed')
  const replacements = new Map()
  for (const entry of await readdir(overlays, { withFileTypes: true })) {
    if (!entry.isFile() || !expected.has(entry.name)) throw new Error('invalid_gotrue_migration_overlay')
    replacements.set(entry.name, await readFile(new URL(entry.name, overlays), 'utf8'))
  }
  const migrations = []
  for (const name of [...expected.keys()].sort()) {
    const raw = await readFile(new URL(name, directory))
    const { digest, version } = expected.get(name)
    if (createHash('sha256').update(raw).digest('hex') !== digest) throw new Error('gotrue_migration_checksum_mismatch')
    migrations.push({ name, version, sql: namespaceSQL(replacements.get(name) ?? raw.toString('utf8')) })
  }
  return migrations
}

export async function applyGoTrueMigrations(client, directory = directoryURL, manifest = manifestURL, overlays = overlayURL) {
  // Verify every file before making any database change, including a skipped one.
  const migrations = await verifiedGoTrueMigrations(directory, manifest, overlays)
  await client.query(`SELECT pg_advisory_lock(hashtextextended('gregale_gotrue_migrations', 0))`)
  try {
    await client.query('CREATE TABLE IF NOT EXISTS auth.schema_migrations (version varchar(255) PRIMARY KEY)')
    const applied = new Set((await client.query('SELECT version FROM auth.schema_migrations')).rows.map(row => row.version))
    let count = 0
    for (const migration of migrations) {
      if (applied.has(migration.version)) continue
      await client.query('BEGIN')
      try {
        await client.query('SET LOCAL search_path = auth, pg_catalog')
        await client.query(migration.sql)
        await client.query('INSERT INTO auth.schema_migrations(version) VALUES($1)', [migration.version])
        await client.query('COMMIT'); count++
      } catch (error) {
        await client.query('ROLLBACK'); throw error
      }
    }
    return count
  } finally {
    await client.query(`SELECT pg_advisory_unlock(hashtextextended('gregale_gotrue_migrations', 0))`)
  }
}
