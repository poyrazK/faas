import pg from 'pg'
import { realpath } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'

export function options(args) {
  const result = { apply: false, batchSize: 100, maxBatches: 1 }
  const flags = new Map([['--before', 'before'], ['--batch-size', 'batchSize'], ['--max-batches', 'maxBatches']])
  for (let i = 0; i < args.length; i++) {
    const flag = args[i]
    if (flag === '--apply' && !result.apply) { result.apply = true; continue }
    const property = flags.get(flag)
    if (!property || result[property + 'Set'] || args[i + 1] === undefined) throw new Error('Invalid cleanup options')
    result[property + 'Set'] = true
    result[property] = property === 'before' ? args[++i] : Number(args[++i])
  }
  return validate(result)
}

function validate({ before, batchSize = 100, maxBatches = 1, apply = false }) {
  if (typeof before !== 'string' || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$/.test(before) ||
      Number(before.slice(0, 4)) < 1 || !Number.isFinite(Date.parse(before)) || new Date(before).toISOString() !== before) throw new Error('An exact UTC cutoff is required')
  for (const value of [batchSize, maxBatches]) if (!Number.isInteger(value) || value < 1 || value > 2147483647) throw new Error('Positive PostgreSQL integer batch bounds are required')
  if (typeof apply !== 'boolean') throw new Error('Invalid cleanup mode')
  return { before, batchSize, maxBatches, apply }
}

const purge = `WITH candidates AS MATERIALIZED (
  SELECT subject, request_key FROM api.note_create_receipts
  WHERE created_at < $1::timestamptz
  ORDER BY created_at, subject, request_key LIMIT $2::integer
  FOR UPDATE SKIP LOCKED
), available AS MATERIALIZED (
  SELECT subject, request_key FROM candidates WHERE pg_try_advisory_xact_lock(
    hashtextextended(jsonb_build_array('gregale.note_create', subject, request_key)::text, 0))
)
DELETE FROM api.note_create_receipts r USING available a
WHERE r.subject=a.subject AND r.request_key=a.request_key
AND r.created_at < $1::timestamptz`

export async function cleanup(connectionString, input) {
  const config = validate(input)
  if (!connectionString) throw new Error('Migration binding required')
  const client = new pg.Client({ connectionString, connectionTimeoutMillis: 5000, query_timeout: 30000 })
  let deleted = 0n, batches = 0
  try {
    await client.connect()
    await client.query(`BEGIN ${config.apply ? 'ISOLATION LEVEL READ COMMITTED' : 'ISOLATION LEVEL REPEATABLE READ READ ONLY'}`)
    const verify = async () => {
      await client.query("SET LOCAL statement_timeout='30s'; SET LOCAL lock_timeout='1s'")
      const owner = await client.query(`SELECT c.relowner=(SELECT oid FROM pg_roles WHERE rolname=current_user) AND NOT c.relforcerowsecurity AS allowed
        FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
        WHERE n.nspname='api' AND c.relname='note_create_receipts' AND c.relkind='r'`)
      if (owner.rows[0]?.allowed !== true) throw new Error('Receipt table owner required')
      const cutoff = await client.query('SELECT $1::timestamptz <= clock_timestamp() AS allowed', [config.before])
      if (!cutoff.rows[0].allowed) throw new Error('Future cleanup cutoff refused')
    }
    await verify()
    const count = async () => (await client.query('SELECT count(*)::text AS n FROM api.note_create_receipts WHERE created_at < $1::timestamptz', [config.before])).rows[0].n
    const eligible = await count()
    if (config.apply) {
      for (let i = 0; i < config.maxBatches; i++) {
        if (i > 0) { await client.query('BEGIN ISOLATION LEVEL READ COMMITTED'); await verify() }
        const result = await client.query(purge, [config.before, config.batchSize])
        await client.query('COMMIT')
        batches++
        deleted += BigInt(result.rowCount)
        if (result.rowCount === 0) break
      }
      await client.query('BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY')
      await verify()
    }
    const remaining = await count()
    await client.query('COMMIT')
    return { mode: config.apply ? 'apply' : 'dry-run', before: config.before, batchSize: config.batchSize, maxBatches: config.maxBatches, eligible, deleted: String(deleted), remaining, batches }
  } catch (error) {
    try { await client.query('ROLLBACK') } catch {}
    throw error
  } finally { await client.end() }
}

if (process.argv[1] && await realpath(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try { console.log(JSON.stringify(await cleanup(process.env.MIGRATION_DATABASE_URL, options(process.argv.slice(2))))) }
  catch { console.error('Receipt cleanup failed: check options, owner migration binding, migrations and lock contention. Earlier committed batches may remain applied.'); process.exitCode = 1 }
}
