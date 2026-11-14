import { createHash } from 'node:crypto'
import { readdir, open, realpath } from 'node:fs/promises'
import { constants } from 'node:fs'
import { fileURLToPath } from 'node:url'

// Mirrors DataAPISingleAppMigrationsMaxFiles/MaxBytes in pkg/api/limits.go.
export const migrationLimits = Object.freeze({ files: 4096, bytes: 16 << 20 })

// Tokenize only enough PostgreSQL syntax to recognize top-level transaction
// control. Quoted identifiers, strings, nested comments and dollar bodies are
// opaque; SQL-language BEGIN ATOMIC bodies retain their internal semicolons.
export function validateMigrationSQL(sql) {
  const tokens = []
  for (let i = 0; i < sql.length;) {
    if (/\s/.test(sql[i])) { i++; continue }
    if (sql.startsWith('--', i)) { const end = sql.indexOf('\n', i + 2); i = end < 0 ? sql.length : end + 1; continue }
    if (sql.startsWith('/*', i)) {
      let depth = 1; i += 2
      while (i < sql.length && depth) {
        if (sql.startsWith('/*', i)) { depth++; i += 2 }
        else if (sql.startsWith('*/', i)) { depth--; i += 2 }
        else i++
      }
      if (depth) throw new Error('Unclosed migration comment')
      continue
    }
    const delimiter = sql.slice(i).match(/^\$(?:[A-Za-z_][A-Za-z_0-9]*)?\$/)?.[0]
    if (delimiter) {
      const end = sql.indexOf(delimiter, i + delimiter.length)
      if (end < 0) throw new Error('Unclosed migration dollar string')
      tokens.push('<literal>'); i = end + delimiter.length; continue
    }
    if (sql[i] === "'" || sql[i] === '"') {
      const quote = sql[i++], escaped = quote === "'" && tokens.at(-1)?.toUpperCase() === 'E'
      let closed = false
      while (i < sql.length) {
        if (escaped && sql[i] === '\\') { i += 2; continue }
        if (sql[i++] !== quote) continue
        if (sql[i] === quote) { i++; continue }
        closed = true; break
      }
      if (!closed) throw new Error('Unclosed migration quoted value')
      tokens.push('<literal>'); continue
    }
    const word = sql.slice(i).match(/^[A-Za-z_][A-Za-z_0-9$]*/)?.[0]
    if (word) { tokens.push(word); i += word.length }
    else tokens.push(sql[i++])
  }
  let statement = [], parens = 0, atomic = 0
  const check = () => {
    const [first, second] = statement
    if (['BEGIN','COMMIT','END','ROLLBACK','ABORT','SAVEPOINT','RELEASE'].includes(first) || first === 'START' && second === 'TRANSACTION' || first === 'PREPARE' && second === 'TRANSACTION' || first === 'SET' && (second === 'TRANSACTION' || statement.slice(1,5).join(' ') === 'SESSION CHARACTERISTICS AS TRANSACTION')) throw new Error('Migration SQL must not control transactions; the runner owns the transaction')
    if (first === 'SET' && statement.some(word => word === 'STANDARD_CONFORMING_STRINGS')) throw new Error('Migration SQL must retain standard_conforming_strings')
    statement = []
  }
  for (const token of tokens) {
    const word = token.toUpperCase()
    if (token === '(') parens++
    if (token === ')') parens--
    if (word === 'ATOMIC' && statement.at(-1) === 'BEGIN' && statement[0] === 'CREATE') atomic++
    else if (atomic && word === 'CASE') atomic++
    else if (atomic && word === 'END') atomic--
    if (token === ';' && parens === 0 && atomic === 0) check()
    else statement.push(word)
  }
  check()
}

export async function migrationFiles(directory, { signal } = {}) {
  signal?.throwIfAborted()
  const names = (await readdir(directory)).sort()
  if (!names.length || names.length > migrationLimits.files || names.some(name => !/^\d{4}_[a-z0-9_]+\.sql$/.test(name))) throw new Error('Use numbered SQL files such as 0001_notes.sql')
  const versions = new Set(), files = []
  let remaining = migrationLimits.bytes
  for (const name of names) {
    signal?.throwIfAborted()
    const version = Number(name.slice(0, 4))
    if (version === 0 || versions.has(version)) throw new Error('Migration versions must be positive and unique')
    versions.add(version)
    const file = await open(new URL(name, directory), constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK)
    try {
      const info = await file.stat()
      if (!info.isFile() || info.size > remaining) throw new Error('Migrations must be regular files totaling at most 16 MiB')
      const buffer = Buffer.alloc(Math.min(info.size + 1, remaining + 1))
      let length = 0
      while (length < buffer.length) {
        signal?.throwIfAborted()
        const { bytesRead } = await file.read(buffer, length, buffer.length - length, length)
        if (!bytesRead) break
        length += bytesRead
      }
      const after = await file.stat()
      if (length !== info.size || after.size !== info.size || after.mtimeMs !== info.mtimeMs || after.ctimeMs !== info.ctimeMs) throw new Error('Migration changed while reading; retry with a stable file')
      const bytes = buffer.subarray(0, length)
      const sql = new TextDecoder('utf-8', { fatal: true }).decode(bytes)
      validateMigrationSQL(sql)
      remaining -= length
      files.push({ version, name, sql, checksum: createHash('sha256').update(bytes).digest('hex') })
    } finally { await file.close() }
  }
  return files
}

export async function migrate(connectionString, directory = new URL('./sql/', import.meta.url), { through, signal } = {}) {
  if (!connectionString) throw new Error('MIGRATION_DATABASE_URL is required')
  const allFiles = await migrationFiles(directory, { signal })
  if (through !== undefined && (!Number.isInteger(through) || !allFiles.some(file => file.version === through))) throw new Error('Selected migration version does not exist')
  const files = through === undefined ? allFiles : allFiles.filter(file => file.version <= through)
  const { default: pg } = await import('pg')
  const client = new pg.Client({ connectionString, connectionTimeoutMillis: 5000 })
  try {
    await client.connect()
    await client.query('BEGIN')
    await client.query('SET LOCAL standard_conforming_strings = on')
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
      signal?.throwIfAborted()
      await client.query('SET LOCAL standard_conforming_strings = on')
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
