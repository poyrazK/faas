import pg from 'pg'
import { createHash } from 'node:crypto'
import { readLocalFile } from './dev-project.mjs'
import { migrationFiles } from './migrate.mjs'
import { pathToFileURL } from 'node:url'

export function parseDataChecks(content) {
  let manifest
  try { manifest = JSON.parse(content) } catch { throw new Error("Data check manifest must contain valid JSON") }
  if (!manifest || manifest.version !== 1 || Object.keys(manifest).some(key => !['version', 'checks'].includes(key)) || !Array.isArray(manifest.checks) || !manifest.checks.length || manifest.checks.length > 100) throw new Error('Use a version 1 manifest with one to 100 data checks')
  const names = new Set()
  for (const check of manifest.checks) {
    if (!check || Object.keys(check).some(key => !['name', 'sql'].includes(key)) || typeof check.name !== 'string' || !check.name.trim() || check.name.length > 128 || typeof check.sql !== 'string' || !check.sql.trim() || names.has(check.name)) throw new Error('Data checks require unique names and SQL queries')
    names.add(check.name)
  }
  return manifest.checks
}

export function fixtureBinding(sql) {
 if (!sql.startsWith('-- gregale-fixture-v1 ')) return null
 const newline = sql.indexOf('\n')
 let header
 try { header = JSON.parse(sql.slice('-- gregale-fixture-v1 '.length, newline)) } catch { throw new Error('Invalid generated fixture header') }
 const hash = value => typeof value === 'string' && /^[a-f0-9]{64}$/.test(value)
 if (!header || Object.keys(header).sort().join(',') !== 'payload_sha256,source_fingerprint' || !hash(header.source_fingerprint) || !hash(header.payload_sha256) || createHash('sha256').update(sql.slice(newline + 1)).digest('hex') !== header.payload_sha256) throw new Error('Generated fixture checksum or source fingerprint is invalid')
 return header.source_fingerprint
}

export async function prepareRehearsal(options, project) {
  if (options.fixture && !options.from_version) throw new Error('SQL fixtures require a selected source migration version')
  const files = await migrationFiles(pathToFileURL(project.migrations + '/'))
  if (options.from_version && (!Number.isInteger(options.from_version) || !files.some(file => file.version === options.from_version))) throw new Error('Selected migration version does not exist')
  let fixtureSQL = project.seedSQL
  if (options.fixture) fixtureSQL = await readLocalFile(options.fixture, 1048576)
  if (options.from_version && (fixtureSQL === null || !fixtureSQL.trim())) throw new Error('Upgrade rehearsal requires --fixture FILE or a configured nonempty seed for the older schema')
  const dataChecks = options.data_check ? parseDataChecks(await readLocalFile(options.data_check)) : []
  return { fixtureSQL, fixtureFingerprint: fixtureSQL === null ? null : fixtureBinding(fixtureSQL), dataChecks, pending: files.filter(file => file.version > options.from_version).map(({ version, name, checksum }) => ({ version, name, checksum })) }
}

// Assertions run before API verification mutates its own test rows. The extended
// protocol permits one SQL statement per check; each has its own read-only TX.
export async function checkUpgradeData(connectionString, checks, signal) {
  const client = new pg.Client({ connectionString, connectionTimeoutMillis: 5000 })
  const results = []
  try {
    await client.connect()
    for (const check of checks) {
      signal?.throwIfAborted()
      try {
        await client.query('BEGIN READ ONLY')
        await client.query("SET LOCAL statement_timeout = '15s'")
        await client.query("SET LOCAL lock_timeout = '5s'")
        const result = await client.query({ text: check.sql, queryMode: 'extended' })
        const passed = result.fields.length === 1 && result.fields[0].name === 'passed' && result.fields[0].dataTypeID === 16 && result.rows.length === 1 && result.rows[0].passed === true
        results.push({ name: check.name, passed, ...(passed ? {} : { error: 'Expected one row containing a boolean passed=true' }) })
      } catch {
        results.push({ name: check.name, passed: false, error: 'Data assertion query failed; inspect its SQL and the upgraded schema' })
      } finally { await client.query('ROLLBACK') }
    }
    return results
  } finally { await client.end() }
}
