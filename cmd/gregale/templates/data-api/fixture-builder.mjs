import pg from 'pg'
import { createHash, createHmac, randomBytes } from 'node:crypto'
import { mkdir, mkdtemp, rename, writeFile, rm, readFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'
import { pathToFileURL } from 'node:url'
import { readLocalFile } from './dev-project.mjs'
import { databaseURL, limits } from './config.mjs'
import { inspectClient, fingerprint } from './types.mjs'

const object = value => value && typeof value === 'object' && !Array.isArray(value)
const fields = (value, allowed) => object(value) && Object.keys(value).every(key => allowed.includes(key))
const identifier = /^[a-z][a-z0-9_]{0,62}$/
const scalar = value => value === null || ['string', 'boolean'].includes(typeof value) || (typeof value === 'number' && Number.isFinite(value))
const quote = value => `"${value.replaceAll('"', '""')}"`
const relation = value => value.split('.').map(quote).join('.')
export const sqlLiteral = value => value === null ? 'NULL' : `E'${String(value).replaceAll('\\', '\\\\').replaceAll("'", "''")}'`
const hash = value => createHash('sha256').update(value).digest('hex')

export function fixturePolicy(content) {
  let policy
  try { policy = JSON.parse(content) } catch { throw new Error('Fixture config must contain valid JSON') }
  if (!fields(policy, ['version', 'tables']) || policy.version !== 1 || !Array.isArray(policy.tables) || !policy.tables.length || policy.tables.length > 50) throw new Error('Use a version 1 fixture config with one to 50 tables')
  const names = new Set()
  for (const table of policy.tables) {
    if (!fields(table, ['table', 'columns', 'mask', 'where', 'limit']) || typeof table.table !== 'string' || !/^api\.[a-z][a-z0-9_]{0,62}$/.test(table.table) || names.has(table.table) || !Array.isArray(table.columns) || !table.columns.length || table.columns.some(column => typeof column !== 'string' || !identifier.test(column)) || new Set(table.columns).size !== table.columns.length) throw new Error('Declare unique api tables with explicit column allowlists')
    names.add(table.table)
    if (table.limit !== undefined && (!Number.isInteger(table.limit) || table.limit < 0 || table.limit > limits.rows)) throw new Error('Fixture root limits must be between zero and the Data API row bound')
    for (const [column, rule] of Object.entries(table.mask ?? {})) {
      if (!fields(rule, ['kind', 'value', 'group']) || !table.columns.includes(column) || !['replace', 'null', 'hash'].includes(rule.kind) || (rule.kind === 'replace' && (!Object.hasOwn(rule, 'value') || !scalar(rule.value) || Object.hasOwn(rule, 'group'))) || (rule.kind === 'hash' && (typeof rule.group !== 'string' || !identifier.test(rule.group) || Object.hasOwn(rule, 'value'))) || (rule.kind === 'null' && (Object.hasOwn(rule, 'value') || Object.hasOwn(rule, 'group')))) throw new Error('Use explicit replace, null, or grouped hash masks on allowlisted columns')
    }
    if ((table.mask !== undefined && !object(table.mask)) || (table.where !== undefined && (!object(table.where) || Object.entries(table.where).some(([column, value]) => !table.columns.includes(column) || !scalar(value))))) throw new Error('Use allowlisted equality filters and a mask object')
  }
  return policy
}

const columnsQuery = `SELECT n.nspname||'.'||c.relname AS relation,c.oid::text AS oid,c.relkind AS kind,
 a.attname AS name,format_type(a.atttypid,a.atttypmod) AS type,a.attnotnull AS required,
 a.attgenerated AS generated,d.oid IS NOT NULL AS defaulted,a.attidentity AS identity,
 t.typname AS base_type,tn.nspname AS type_schema,a.atttypmod AS modifier,
 pg_get_serial_sequence(format('%I.%I',n.nspname,c.relname),a.attname) AS sequence,
 (SELECT seqincrement::text FROM pg_sequence WHERE seqrelid=pg_get_serial_sequence(format('%I.%I',n.nspname,c.relname),a.attname)::regclass) AS sequence_increment,
 (SELECT seqmin::text FROM pg_sequence WHERE seqrelid=pg_get_serial_sequence(format('%I.%I',n.nspname,c.relname),a.attname)::regclass) AS sequence_min
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 JOIN pg_attribute a ON a.attrelid=c.oid JOIN pg_type t ON t.oid=a.atttypid JOIN pg_namespace tn ON tn.oid=t.typnamespace
 LEFT JOIN pg_attrdef d ON d.adrelid=c.oid AND d.adnum=a.attnum
 WHERE n.nspname='api' AND c.relname=ANY($1::text[]) AND a.attnum>0 AND NOT a.attisdropped
 ORDER BY c.relname,a.attnum LIMIT ${limits.columns + 1}`
const keysQuery = `SELECT n.nspname||'.'||c.relname AS relation,x.contype::text AS kind,x.conname AS name,
 ARRAY(SELECT a.attname::text FROM unnest(x.conkey) WITH ORDINALITY k(num,seq)
 JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum=k.num ORDER BY k.seq) AS columns,
 rn.nspname||'.'||rc.relname AS parent,
 ARRAY(SELECT a.attname::text FROM unnest(x.confkey) WITH ORDINALITY k(num,seq)
 JOIN pg_attribute a ON a.attrelid=rc.oid AND a.attnum=k.num ORDER BY k.seq) AS parent_columns
 FROM pg_constraint x JOIN pg_class c ON c.oid=x.conrelid JOIN pg_namespace n ON n.oid=c.relnamespace
 LEFT JOIN pg_class rc ON rc.oid=x.confrelid LEFT JOIN pg_namespace rn ON rn.oid=rc.relnamespace
 WHERE n.nspname='api' AND c.relname=ANY($1::text[]) AND x.contype IN ('p','u','f') AND x.conparentid=0
 UNION ALL
 SELECT n.nspname||'.'||c.relname AS relation,
 CASE WHEN i.indpred IS NULL AND i.indexprs IS NULL THEN 'u' ELSE 'unsupported_unique' END AS kind,
 ic.relname AS name,
 ARRAY(SELECT a.attname::text FROM unnest(i.indkey::smallint[]) WITH ORDINALITY k(num,seq)
 LEFT JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum=k.num WHERE k.seq<=i.indnkeyatts ORDER BY k.seq) AS columns,
 NULL::text AS parent,NULL::text[] AS parent_columns
 FROM pg_index i JOIN pg_class c ON c.oid=i.indrelid JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_class ic ON ic.oid=i.indexrelid
 WHERE n.nspname='api' AND c.relname=ANY($1::text[]) AND i.indisunique
 AND NOT EXISTS(SELECT 1 FROM pg_constraint x WHERE x.conindid=i.indexrelid AND x.contype IN ('p','u'))
 ORDER BY relation,name LIMIT ${limits.relations + 1}`

function metadata(policy, columns, keys) {
  if (columns.length > limits.columns || keys.length > limits.relations) throw new Error('Fixture schema exceeds the Data API catalog bounds')
  const tables = new Map()
  for (const config of policy.tables) {
    const all = columns.filter(column => column.relation === config.table)
    if (!all.length || !['r', 'p'].includes(all[0].kind)) throw new Error('Fixture tables must be existing ordinary or partitioned api tables')
    const selected = config.columns.map(name => {
      const column = all.find(column => column.name === name)
      if (!column || column.generated) throw new Error('Allowlisted columns must exist and be directly insertable')
      if (column.sequence && (column.sequence_increment !== '1' || column.sequence_min !== '1')) throw new Error('Fixture sequence restoration requires ascending sequences with increment and minimum one')
      return column
    })
    const tableKeys = keys.filter(key => key.relation === config.table)
    if (tableKeys.some(key => key.kind === 'unsupported_unique')) throw new Error('Partial or expression unique indexes require a hand-authored fixture')
    const primary = tableKeys.find(key => key.kind === 'p')
    if (!primary || tableKeys.some(key => key.columns.some(column => !config.columns.includes(column)))) throw new Error('Include the primary, unique, and foreign-key columns in each allowlist')
    if (all.some(column => column.required && !column.defaulted && !column.identity && !column.generated && !config.columns.includes(column.name))) throw new Error('Allowlist or mask every required column without a database default')
    for (const [name, rule] of Object.entries(config.mask ?? {})) {
      const column = selected.find(column => column.name === name)
      const keyed = tableKeys.some(key => key.columns.includes(name)) || keys.some(key => key.kind === 'f' && key.parent === config.table && key.parent_columns.includes(name))
      if (keyed && rule.kind !== 'hash') throw new Error('Key columns support keep or grouped hash masking only')
      if ((rule.kind === 'null' || rule.kind === 'replace' && rule.value === null) && column.required) throw new Error('Null masks require nullable columns')
      if (rule.kind === 'hash' && (column.type_schema !== 'pg_catalog' || !['text', 'varchar'].includes(column.base_type) || column.base_type === 'varchar' && column.modifier >= 0 && column.modifier - 4 < 32)) throw new Error('Hash masks require text or varchar columns with space for 32 characters')
    }
    tables.set(config.table, { config, columns: selected, primary: primary.columns, keys: tableKeys, rows: new Map() })
  }
  const foreign = keys.filter(key => key.kind === 'f')
  for (const key of foreign) {
    const child = tables.get(key.relation), parent = tables.get(key.parent)
    if (!parent) continue // An active reference to an undeclared parent fails during closure.
    for (let i = 0; i < key.columns.length; i++) {
      if (!parent.config.columns.includes(key.parent_columns[i])) throw new Error('Referenced key columns must be allowlisted')
      const left = child.config.mask?.[key.columns[i]], right = parent.config.mask?.[key.parent_columns[i]]
      if ((left?.kind ?? 'keep') !== (right?.kind ?? 'keep') || left?.group !== right?.group) throw new Error('Foreign-key pairs require the same masking kind and hash group')
    }
  }
  return { tables, foreign }
}

async function selectedRows(client, table, filters, limit) {
  const values = []
  const terms = filters.map(([column, value]) => {
    const info = table.columns.find(info => info.name === column)
    if (value === null) return `${quote(column)} IS NULL`
    values.push(typeof value === 'string' ? value : String(value))
    return `${quote(column)}=$${values.length}::${info.type}`
  })
  values.push(limit)
  const bytes = table.columns.map(column => `COALESCE(octet_length(s.${quote(column.name)}::text),0)::bigint`).join('+')
  const query = `WITH selected AS (SELECT ${table.config.columns.map(quote).join(',')} FROM ${relation(table.config.table)}${terms.length ? ` WHERE ${terms.join(' AND ')}` : ''} ORDER BY ${table.primary.map(quote).join(',')} LIMIT $${values.length}), budget AS (SELECT COALESCE(sum(${bytes}),0) AS bytes FROM selected s) SELECT ${table.columns.map(column => `CASE WHEN b.bytes<=${limits.bodyBytes} THEN s.${quote(column.name)}::text ELSE NULL END AS ${quote(column.name)}`).join(',')},b.bytes AS "__gregale_bytes" FROM selected s CROSS JOIN budget b`
  const rows = (await client.query(query, values)).rows
  if (rows.some(row => Number(row.__gregale_bytes) > limits.bodyBytes)) throw new Error('Selected source values exceed the fixture byte bound; narrow the roots')
  for (const row of rows) delete row.__gregale_bytes
  return rows
}

async function collectRows(client, tables, foreign) {
  const queue = [], active = new Set(), links = []
  let bytes = 0
  const add = (table, row) => {
    const key = JSON.stringify(table.primary.map(column => row[column]))
    if (!table.rows.has(key)) {
      if (queue.length >= limits.rows) throw new Error('Related fixture rows exceed the Data API row bound; narrow the roots')
      bytes += Object.values(row).reduce((sum, value) => sum + (value === null ? 0 : Buffer.byteLength(value)), 0)
      if (bytes > limits.bodyBytes) throw new Error('Related source values exceed the fixture byte bound; narrow the roots')
      table.rows.set(key, row); queue.push({ table, row })
    }
    return table.rows.get(key)
  }
  for (const table of tables.values()) {
    const limit = table.config.limit ?? 20
    if (limit) for (const row of await selectedRows(client, table, Object.entries(table.config.where ?? {}), limit)) add(table, row)
  }
  for (let index = 0; index < queue.length; index++) {
    const { table, row } = queue[index]
    for (const key of foreign.filter(key => key.relation === table.config.table)) {
      if (key.columns.some(column => row[column] === null)) continue
      const parent = tables.get(key.parent)
      if (!parent) throw new Error('A selected row references an undeclared parent table; extend the allowlist')
      const rows = await selectedRows(client, parent, key.parent_columns.map((column, i) => [column, row[key.columns[i]]]), 2)
      if (rows.length !== 1) throw new Error('A required parent row is missing or invisible to the source account')
      const parentRow = add(parent, rows[0]); active.add(key); links.push({ key, child: row, parent: parentRow })
    }
    for (const key of foreign.filter(key => key.parent === table.config.table && tables.has(key.relation))) {
      if (key.parent_columns.some(column => row[column] === null)) continue
      const child = tables.get(key.relation)
      const rows = await selectedRows(client, child, key.columns.map((column, i) => [column, row[key.parent_columns[i]]]), limits.rows + 1)
      for (const childRow of rows) add(child, childRow)
    }
  }
  if (!queue.length) throw new Error('Fixture selection contains no rows')
  return { active, links }
}

async function maskRows(client, tables, links) {
  const salt = randomBytes(32), masked = new Map()
  for (const table of tables.values()) {
    const converted = new Map()
    for (const row of table.rows.values()) {
      const result = { ...row }
      for (const [name, rule] of Object.entries(table.config.mask ?? {})) {
        const column = table.columns.find(column => column.name === name)
        if (rule.kind === 'null' || rule.kind === 'replace' && rule.value === null) result[name] = null
        else if (rule.kind === 'hash') result[name] = row[name] === null ? null : createHmac('sha256', salt).update(JSON.stringify([rule.group, row[name]])).digest('hex').slice(0, 32)
        else {
          const key = JSON.stringify([name, rule.value])
          if (!converted.has(key)) converted.set(key, (await client.query(`SELECT $1::${column.type}::text AS value`, [String(rule.value)])).rows[0].value)
          result[name] = converted.get(key)
        }
      }
      masked.set(row, result)
    }
    for (const key of table.keys.filter(key => key.kind !== 'f')) {
      const seen = new Set()
      for (const row of table.rows.values()) {
        const values = key.columns.map(column => masked.get(row)[column])
        if (key.kind === 'u' && values.some(value => value === null)) continue
        const identity = JSON.stringify(values)
        if (seen.has(identity)) throw new Error('Masking produced colliding primary or unique keys')
        seen.add(identity)
      }
    }
  }
  for (const { key, child, parent } of links) {
    const childTable = tables.get(key.relation)
    for (let i = 0; i < key.columns.length; i++) if (childTable.config.mask?.[key.columns[i]] && masked.get(child)[key.columns[i]] !== masked.get(parent)[key.parent_columns[i]]) throw new Error('Masked foreign-key values do not match their referenced rows')
  }
  return masked
}

function tableOrder(tables, active) {
  const ordered = [], visited = new Set(), visiting = new Set()
  const visit = table => {
    if (visited.has(table)) return
    if (visiting.has(table)) throw new Error('Fixture export requires an acyclic table dependency graph')
    visiting.add(table)
    for (const key of active) if (key.relation === table) visit(key.parent)
    visiting.delete(table); visited.add(table); ordered.push(tables.get(table))
  }
  for (const name of tables.keys()) visit(name)
  return ordered
}

function renderPackage(tables, active, masked, snapshot) {
  const sourceFingerprint = fingerprint(snapshot), statements = [], checks = [], counts = []
  for (const table of tableOrder(tables, active)) {
    const rows = [...table.rows.values()].map(row => masked.get(row))
    counts.push({ table: table.config.table, rows: rows.length, columns: table.config.columns, masks: Object.fromEntries(Object.entries(table.config.mask ?? {}).map(([column, rule]) => [column, { kind: rule.kind, ...(rule.kind === 'hash' ? { group: rule.group } : {}) }])) })
    if (!rows.length) continue
    const typed = (column, value) => `${sqlLiteral(value)}::${table.columns.find(info => info.name === column).type}`
    for (const row of rows) statements.push(`INSERT INTO ${relation(table.config.table)} (${table.config.columns.map(quote).join(',')}) OVERRIDING SYSTEM VALUE VALUES (${table.columns.map(column => typed(column.name, row[column.name])).join(',')});`)
    for (const column of table.columns.filter(column => column.sequence)) statements.push(`SELECT pg_catalog.setval(pg_catalog.pg_get_serial_sequence(${sqlLiteral(table.config.table)},${sqlLiteral(column.name)}),GREATEST(COALESCE(MAX(${quote(column.name)}),1),1),COALESCE(MAX(${quote(column.name)})>=1,false)) FROM ${relation(table.config.table)};`)
    const predicate = rows.map(row => `(${table.primary.map(column => `${quote(column)}=${typed(column, row[column])}`).join(' AND ')})`).join(' OR ')
    checks.push({ name: `${table.config.table} exported keys preserved`, sql: `SELECT count(*)=${rows.length} AS passed FROM ${relation(table.config.table)} WHERE ${predicate}` })
  }
  for (const key of active) checks.push({ name: `${key.relation} ${key.name} references preserved`, sql: `SELECT NOT EXISTS (SELECT 1 FROM ${relation(key.relation)} c WHERE ${key.columns.map(column => `c.${quote(column)} IS NOT NULL`).join(' AND ')} AND NOT EXISTS (SELECT 1 FROM ${relation(key.parent)} p WHERE ${key.columns.map((column, i) => `c.${quote(column)}=p.${quote(key.parent_columns[i])}`).join(' AND ')})) AS passed` })
  if (checks.length > 100) throw new Error('Generated assertions exceed the rehearsal check bound; narrow the declared tables')
  for (const check of checks) if (check.name.length > 128) check.name = `${check.name.slice(0, 111)} ${hash(check.name).slice(0, 16)}`
  const payload = statements.join('\n') + '\n'
  const header = { source_fingerprint: sourceFingerprint, payload_sha256: hash(payload) }
  const fixture = `-- gregale-fixture-v1 ${JSON.stringify(header)}\n${payload}`
  const assertions = JSON.stringify({ version: 1, checks }, null, 2) + '\n'
  if (Buffer.byteLength(fixture) > limits.bodyBytes || Buffer.byteLength(assertions) > 65536) throw new Error('Generated fixture or assertions exceed rehearsal file bounds; narrow the roots')
  const contract = JSON.stringify({ version: 1, fingerprint: sourceFingerprint, snapshot }) + '\n'
  if (Buffer.byteLength(contract) > limits.outputBytes) throw new Error('Source API contract exceeds the schema comparison output bound')
  const files = {
    'fixture.sql': fixture,
    'data-checks.json': assertions,
    'contract.json': contract,
    'fixture.json': JSON.stringify({ version: 1, source_fingerprint: sourceFingerprint, fixture_sha256: hash(fixture), tables: counts, coverage: 'declared columns and relationships', masking: 'explicit rules; grouped hashes use an unrecorded random salt' }, null, 2) + '\n',
  }
  return { files, source_fingerprint: sourceFingerprint, tables: counts.map(({ table, rows }) => ({ table, rows })), rows: counts.reduce((sum, table) => sum + table.rows, 0) }
}

export async function buildFixture(connection, policy, apiRole) {
  const client = new pg.Client({ connectionString: databaseURL(connection), connectionTimeoutMillis: limits.queryMs, query_timeout: limits.queryMs })
  await client.connect()
  try {
    await client.query('BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY')
    await client.query("SET LOCAL statement_timeout='15s'; SET LOCAL lock_timeout='5s'; SET LOCAL search_path=pg_catalog; SET LOCAL timezone='UTC';")
    const sourceRole = (await client.query('SELECT current_user AS role')).rows[0].role
    if (apiRole) await client.query(`SET LOCAL ROLE ${quote(apiRole)}`)
    const snapshot = await inspectClient(client, ['api'])
    if (apiRole) await client.query(`SET LOCAL ROLE ${quote(sourceRole)}`)
    const names = policy.tables.map(table => table.table.slice(4))
    const columns = (await client.query(columnsQuery, [names])).rows
    const keys = (await client.query(keysQuery, [names])).rows
    const { tables, foreign } = metadata(policy, columns, keys)
    const { active, links } = await collectRows(client, tables, foreign)
    const masked = await maskRows(client, tables, links)
    const result = renderPackage(tables, active, masked, snapshot)
    await client.query('COMMIT')
    return result
  } finally { await client.end() }
}

export async function runFixtureBuilder(options) {
  if (Number(process.versions.node.split('.')[0]) < 22) throw new Error('Node.js 22+ is required')
  const policy = fixturePolicy(await readLocalFile(options.config))
  const connection = process.env[options.database_url_env]
  if (!connection) throw new Error('Configure the selected database URL environment variable')
  const result = await buildFixture(connection, policy, options.api_role)
  await mkdir(dirname(options.output), { recursive: true })
  const staging = await mkdtemp(resolve(dirname(options.output), '.gregale-fixture-'))
  let reserved = false
  try {
    for (const [name, content] of Object.entries(result.files)) await writeFile(resolve(staging, name), content, { mode: 0o600, flag: 'wx' })
    await mkdir(options.output, { mode: 0o700 }) // Exclusive reservation; existing packages are never replaced.
    reserved = true
    await rename(staging, options.output) // Publish all four files together over our empty reservation.
    reserved = false
  } catch {
    if (reserved) await rm(options.output, { recursive: true, force: true })
    throw new Error('Could not publish the fixture package; choose a new writable output directory')
  } finally { await rm(staging, { recursive: true, force: true }) }
  const report = { version: 1, status: 'built', output: options.output, source_fingerprint: result.source_fingerprint, rows: result.rows, tables: result.tables }
  if (options.json) console.log(JSON.stringify(report))
  else console.log(`Fixture built: ${options.output}\n${result.rows} rows; source contract ${result.source_fingerprint}\nUse fixture.sql, contract.json, and data-checks.json with migration preview.`)
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try { await runFixtureBuilder(JSON.parse(await readFile(process.argv[2], 'utf8'))) }
  catch { console.error('Fixture build failed: check the config, source access, column/key allowlists, masking rules, related-row bounds and new output directory.'); process.exitCode = 1 }
}
