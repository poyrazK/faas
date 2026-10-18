import pg from 'pg'

// Mirrored from pkg/api/limits.go; checked by receipt-diagnostics.test.mjs.
export const receiptDiagnosticsLimits = Object.freeze({ connectMs: 5000, statementMs: 5000, lockMs: 1000, queryMs: 6000 })

const unavailable = (contract, reason) => ({ contract: contract.contract, table: `api.${contract.table}`, status: 'unavailable', reason })
const reasonFor = error => error?.code === '57014' ? 'statement_timeout'
  : error?.code === '55P03' ? 'lock_timeout'
  : error?.code === '42P01' ? 'missing_table' : 'query_failed'

async function inspect(client, contract) {
  // Identifiers come only from the package's reviewed contract list. No CLI
  // table/schema options are accepted. Quote even these trusted identifiers.
  if (!/^[a-z][a-z0-9_]{0,62}$/.test(contract.table)) throw new Error('Invalid diagnostic contract')
  const relation = `api."${contract.table}"`
  try {
    await client.query('BEGIN ISOLATION LEVEL READ COMMITTED READ ONLY')
    await client.query("SELECT pg_catalog.set_config('statement_timeout',$1,true), pg_catalog.set_config('lock_timeout',$2,true), pg_catalog.set_config('search_path','pg_catalog',true), pg_catalog.set_config('row_security','off',true)",
      [String(receiptDiagnosticsLimits.statementMs), String(receiptDiagnosticsLimits.lockMs)])
    const verify = async () => {
      const { rows } = await client.query(`SELECT c.relowner=(SELECT oid FROM pg_roles WHERE rolname=current_user) AS owner,
        c.relkind='r' AND NOT c.relforcerowsecurity
        AND NOT EXISTS(SELECT 1 FROM pg_inherits WHERE inhparent=c.oid OR inhrelid=c.oid)
        AND EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid=c.oid AND attname='expires_at'
          AND atttypid='timestamptz'::regtype AND NOT attisdropped) AS supported
        FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='api' AND c.relname=$1`, [contract.table])
      if (!rows[0]) return 'missing_table'
      if (!rows[0].owner) return 'not_owner'
      if (!rows[0].supported) return 'unsupported_table'
      return null
    }
    let failure = await verify()
    if (failure) { await client.query('ROLLBACK'); return unavailable(contract, failure) }
    // Hold ownership/shape stable through aggregation. ACCESS SHARE conflicts
    // with DDL, but does not take row/advisory locks or block normal mutations.
    await client.query(`LOCK TABLE ONLY ${relation} IN ACCESS SHARE MODE`)
    failure = await verify()
    if (failure) { await client.query('ROLLBACK'); return unavailable(contract, failure) }
    const { rows: [row] } = await client.query(`WITH observation AS (SELECT statement_timestamp() AS at), counts AS (
      SELECT count(*)::text AS total, count(*) FILTER(WHERE expires_at > o.at)::text AS active,
        count(*) FILTER(WHERE expires_at <= o.at)::text AS expired,
        count(*) FILTER(WHERE expires_at IS NULL)::text AS invalid,
        min(expires_at) FILTER(WHERE expires_at <= o.at) AS oldest
      FROM ONLY ${relation} CROSS JOIN observation o
    ) SELECT o.at AS observed_at, c.total, c.active, c.expired, c.invalid, c.oldest,
      floor(extract(epoch FROM (o.at-c.oldest))*1000)::bigint::text AS age_ms,
      pg_table_size($1::regclass)::text AS table_bytes, pg_indexes_size($1::regclass)::text AS index_bytes,
      pg_total_relation_size($1::regclass)::text AS total_bytes FROM observation o CROSS JOIN counts c`, [`api.${contract.table}`])
    if (row.invalid !== '0') { await client.query('ROLLBACK'); return unavailable(contract, 'unsupported_table') }
    await client.query('COMMIT')
    return { contract: contract.contract, table: `api.${contract.table}`, status: 'ok', observedAt: row.observed_at.toISOString(),
      totalCount: row.total, activeCount: row.active, expiredCount: row.expired,
      oldestExpiredAt: row.oldest?.toISOString() ?? null, oldestExpiredAgeMs: row.age_ms,
      tableBytes: row.table_bytes, indexBytes: row.index_bytes, totalBytes: row.total_bytes }
  } catch (error) {
    try { await client.query('ROLLBACK') } catch {}
    return unavailable(contract, reasonFor(error))
  }
}

// Cross-tenant aggregates are available only through the stable table owner's
// migration binding. No serving-role grants or HTTP endpoints are added.
export async function inspectReceipts(connectionString, contracts) {
  if (!connectionString) return { version: 1, status: 'incomplete', receipts: contracts.map(c => unavailable(c, 'migration_binding_required')) }
  let client
  try {
    client = new pg.Client({ connectionString, connectionTimeoutMillis: receiptDiagnosticsLimits.connectMs,
      query_timeout: receiptDiagnosticsLimits.queryMs, application_name: 'gregale-receipt-diagnostics' })
  } catch { return { version: 1, status: 'incomplete', receipts: contracts.map(c => unavailable(c, 'connection_failed')) } }
  client.on('error', () => {}) // Report fixed reasons; never expose driver/credential details.
  try {
    try { await client.connect() }
    catch { return { version: 1, status: 'incomplete', receipts: contracts.map(c => unavailable(c, 'connection_failed')) } }
    const receipts = []
    for (const contract of contracts) receipts.push(await inspect(client, contract))
    return { version: 1, status: receipts.every(r => r.status === 'ok') ? 'ok' : 'incomplete', receipts }
  } finally { await client.end().catch(() => {}) }
}

export async function runDiagnostics(diagnostics, options) {
  try {
    const report = await diagnostics(process.env.MIGRATION_DATABASE_URL, options(process.argv.slice(2)))
    console.log(JSON.stringify(report))
    if (report.status !== 'ok') process.exitCode = 1
  } catch {
    console.log(JSON.stringify({ version: 1, status: 'error', reason: 'invalid_options_or_configuration' }))
    process.exitCode = 1
  }
}
