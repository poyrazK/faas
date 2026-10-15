import pg from 'pg'
import { createHash } from 'node:crypto'
import { readLocalFile } from './dev-project.mjs'
import { requestStore } from './dev-requests.mjs'
import { collectionScenarios } from './request-replay.mjs'
import { fixtureBinding } from './dev-upgrade.mjs'
import { limits } from './config.mjs'
const hash = value => createHash('sha256').update(value).digest('hex')
const quote = value => `"${value.replaceAll('"', '""')}"`
const hashPattern = /^[a-f0-9]{64}$/
const nodeTypes = new Set(['Seq Scan', 'Index Scan', 'Index Only Scan', 'Bitmap Index Scan', 'Bitmap Heap Scan', 'Sort', 'Incremental Sort', 'Limit', 'Aggregate', 'Nested Loop', 'Hash Join', 'Merge Join', 'Hash', 'Append', 'Merge Append', 'CTE Scan', 'Result', 'Function Scan', 'Subquery Scan', 'Gather', 'Gather Merge', 'Memoize', 'Materialize', 'Unique', 'Group', 'WindowAgg', 'SetOp', 'Recursive Union', 'WorkTable Scan', 'Values Scan', 'Tid Scan', 'Tid Range Scan', 'Foreign Scan', 'Custom Scan', 'ModifyTable', 'LockRows', 'ProjectSet'])
const numeric = value => typeof value === 'number' && Number.isFinite(value) && value >= 0

export async function prepareExplain(options, project) {
 if ((options.plan_check && (!options.plan_baseline || !options.plan_budget)) || (!options.plan_check && options.plan_budget)) throw new Error('Budget checks require a baseline and budget file')
 const budget = options.plan_check ? queryBudgets(JSON.parse(await readLocalFile(options.plan_budget, 65536))) : null
 const collection = await requestStore(options.collection, { identities: Object.keys(project.identities) }).read(true)
 const scenarios = collectionScenarios(collection, options.plan_scenario)
 const seedSQL = options.fixture ? await readLocalFile(options.fixture, limits.bodyBytes) : project.seedSQL
 const fixtureFingerprint = seedSQL === null ? null : fixtureBinding(seedSQL)
 let baseline
 if (options.plan_baseline) {
  try { baseline = validatePlanBaseline(JSON.parse(await readLocalFile(options.plan_baseline, limits.outputBytes))) }
  catch { throw new Error('Use a successful version 1 data-api explain JSON report as the baseline') }
 }
 return { scenarios, seedSQL, fixtureFingerprint, baseline, budget,
  workload_fingerprint: hash(JSON.stringify(scenarios.flatMap(scenario => scenario.requests.map(request => [scenario.name, request.name, request.method, request.path, request.identity, request.expectedSchemaFingerprint ?? null])))),
  dataset_fingerprint: hash(JSON.stringify([seedSQL, project.identities])),
 }
}
export function validatePlanBaseline(value) {
 if (!value || value.version !== 1 || value.command !== 'data-api explain' || value.status !== 'passed' || value.cleanup !== 'passed' || !hashPattern.test(value.schema_fingerprint) || !hashPattern.test(value.workload_fingerprint) || !hashPattern.test(value.dataset_fingerprint) || !Array.isArray(value.results) || value.results.length > 100) throw new Error('Invalid plan baseline')
 const keys = new Set()
 for (const result of value.results) {
  const key = `${result.scenario}\0${result.name}`
  if (typeof result.scenario !== 'string' || typeof result.name !== 'string' || !result.name || keys.has(key) || !['planned', 'skipped'].includes(result.status) || result.status === 'planned' && (!hashPattern.test(result.query_fingerprint) || !result.plan || !numeric(result.plan.total_cost))) throw new Error('Invalid baseline query')
  keys.add(key)
 }
 return value
}

const metadataQuery = `SELECT c.relname AS name,c.relkind AS kind,GREATEST(c.reltuples,0)::float8 AS rows,
 ARRAY(SELECT a.attname::text FROM pg_attribute a WHERE a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped ORDER BY a.attnum) AS columns,
 ARRAY(SELECT pg_get_expr(p.polqual,p.polrelid) FROM pg_policy p WHERE p.polrelid=c.oid AND p.polqual IS NOT NULL ORDER BY p.polname) AS policies
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='api' AND c.relkind IN ('r','p','v','m') ORDER BY c.relname LIMIT ${limits.relations + 1}`
const indexQuery = `SELECT c.relname AS relation,ic.relname AS name,am.amname AS method,i.indisvalid AS valid,i.indpred IS NOT NULL AS partial,
 ARRAY(SELECT a.attname::text FROM unnest(i.indkey::smallint[]) WITH ORDINALITY k(num,seq) LEFT JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum=k.num WHERE k.seq<=i.indnkeyatts ORDER BY k.seq) AS columns,
 ARRAY(SELECT (o.flag & 1)=1 FROM unnest(i.indoption::smallint[]) WITH ORDINALITY o(flag,seq) WHERE o.seq<=i.indnkeyatts ORDER BY o.seq) AS descending
 FROM pg_index i JOIN pg_class c ON c.oid=i.indrelid JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_class ic ON ic.oid=i.indexrelid JOIN pg_am am ON am.oid=ic.relam
 WHERE n.nspname='api' ORDER BY c.relname,ic.relname LIMIT ${limits.relations + 1}`
export async function plannerMetadata(connectionString) {
 const client = new pg.Client({ connectionString, connectionTimeoutMillis: limits.queryMs, query_timeout: limits.queryMs })
 try {
  await client.connect()
  const tables = (await client.query(metadataQuery)).rows
  if (tables.length > limits.relations) throw new Error('Planner relation catalog exceeds bounds')
  // Statistics are refreshed only on this command's disposable database.
  for (const table of tables.filter(table => ['r', 'p'].includes(table.kind))) await client.query(`ANALYZE "api".${quote(table.name)}`)
  const refreshed = (await client.query(metadataQuery)).rows
  const indexes = (await client.query(indexQuery)).rows
  if (indexes.length > limits.relations) throw new Error('Planner index catalog exceeds bounds')
  return { tables: new Map(refreshed.map(table => [table.name, table])), indexes }
 } finally { await client.end() }
}

export function readQuery(request, metadata) {
 if (request.method !== 'GET') return { reason: 'Only saved GET relation reads are planned; writes are not replayed' }
 if (request.path.includes('{{') || request.path.includes('}}')) return { reason: 'Unresolved capture variables require a concrete read query' }
 const url = new URL(request.path, 'http://127.0.0.1')
 const name = decodeURIComponent(url.pathname.slice('/rest/v1/'.length))
 if (!metadata.tables.has(name)) return { reason: 'Only direct API relation reads are supported; RPC and metadata routes are skipped' }
 if (request.expect.status >= 400) return { reason: 'Expected rejection is an authorization assertion, not a successful read query' }
 const table = metadata.tables.get(name), equality = [], range = [], order = []
 for (const [key, value] of url.searchParams) {
  if (table.columns.includes(key)) {
   const operator = value.split('.')[0]
   if (operator === 'eq' || value === 'is.null') equality.push(key)
   else if (['lt', 'lte', 'gt', 'gte'].includes(operator)) range.push(key)
  }
 }
 for (const item of (url.searchParams.get('order') ?? '').split(',')) {
  const [column, direction = 'asc', ...modifiers] = item.split('.')
  if (table.columns.includes(column) && ['asc', 'desc'].includes(direction) && !modifiers.length) order.push({ column, direction })
 }
 return { name, equality, range, order }
}

export function redactPlan(raw, metadata) {
 let count = 0
 const visit = (node, depth = 0) => {
  if (!node || depth > 64 || ++count > limits.relations || !numeric(node['Total Cost']) || !numeric(node['Plan Rows']) || !Array.isArray(node.Plans ?? [])) throw new Error('Unsupported planner document')
  const type = nodeTypes.has(node['Node Type']) ? node['Node Type'] : 'Other'
  const relation = node.Schema === 'api' && metadata.tables.has(node['Relation Name']) ? `api.${node['Relation Name']}` : undefined
  const index = metadata.indexes.some(index => index.name === node['Index Name']) ? node['Index Name'] : undefined
  return { node_type: type, total_cost: node['Total Cost'], estimated_rows: node['Plan Rows'], ...(numeric(node['Startup Cost']) ? { startup_cost: node['Startup Cost'] } : {}), ...(relation ? { relation } : {}), ...(index ? { index } : {}), children: (node.Plans ?? []).map(child => visit(child, depth + 1)) }
 }
 return visit(raw)
}
const walk = node => [node, ...node.children.flatMap(walk)]
export function planAdvice(plan, query, metadata) {
 const findings = [], suggestions = []
 for (const node of walk(plan)) {
  if (node.node_type === 'Seq Scan' && node.relation) {
   const table = metadata.tables.get(node.relation.slice(4))
   findings.push({ code: 'sequential_scan', relation: node.relation, relation_rows: table.rows, estimated_result_rows: node.estimated_rows, severity: table.rows >= 10000 ? 'warning' : 'info', note: table.rows >= 10000 ? 'Review this scan with representative data and selective indexes' : 'Sequential scans can be appropriate for small relations' })
  }
  if (['Sort', 'Incremental Sort'].includes(node.node_type)) {
   const rows = Math.max(node.estimated_rows, ...node.children.map(child => child.estimated_rows))
   findings.push({ code: 'explicit_sort', estimated_input_rows: rows, severity: rows >= 1000 ? 'warning' : 'info' })
  }
 }
 const table = metadata.tables.get(query.name)
 const policyEquality = (table.policies ?? []).flatMap(expression => {
  // Only simple direct JWT equality policies; complex joins require manual review.
  const match = /^\(*\s*"?([a-z][a-z0-9_]*)"?\s*=.*current_setting\(/s.exec(expression)
  return match && table.columns.includes(match[1]) && !/\bSELECT\b/i.test(expression) ? [match[1]] : []
 })
 const columns = []
 for (const column of [...policyEquality, ...query.equality]) if (!columns.some(item => item.column === column)) columns.push({ column, direction: 'asc' })
 for (const item of query.order) if (!columns.some(column => column.column === item.column)) columns.push(item)
 for (const column of query.range) if (!columns.some(item => item.column === column)) columns.push({ column, direction: 'asc' })
 const hasConcern = findings.some(finding => finding.code === 'sequential_scan' && finding.relation === `api.${query.name}`) || findings.some(finding => finding.code === 'explicit_sort')
 const covered = metadata.indexes.some(index => index.relation === query.name && index.method === 'btree' && index.valid && !index.partial && columns.every((column, i) => column.column === index.columns[i]) && ([false, true].some(reverse => columns.every((column, i) => !query.order.some(order => order.column === column.column) || (Boolean(index.descending?.[i]) !== reverse) === (column.direction === 'desc')))))
 if (hasConcern && columns.length && columns.length <= 32 && !covered) {
  const name = `gregale_advisor_${query.name.slice(0, 20)}_${hash(JSON.stringify(columns)).slice(0, 12)}`
  suggestions.push({ kind: 'btree_candidate', relation: `api.${query.name}`, columns, sql: `CREATE INDEX ${quote(name)} ON "api".${quote(query.name)} (${columns.map(column => `${quote(column.column)} ${column.direction.toUpperCase()}`).join(',')});`, note: 'Review selectivity, RLS, existing indexes, write cost and null ordering; this suggestion is not applied' })
 }
 return { findings, suggestions }
}

async function planDocument(url, token, signal) {
 const response = await fetch(url, { headers: { Authorization: `Bearer ${token}`, Accept: 'application/vnd.pgrst.plan+json; for="application/json"; options=verbose' }, redirect: 'error', signal: AbortSignal.any([signal, AbortSignal.timeout(limits.queryMs + 5000)]) })
 if (!response.ok) throw new Error('Plan request was refused')
 const reader = response.body.getReader(), chunks = []; let size = 0
 try {
  for (;;) { const { done, value } = await reader.read(); if (done) break; size += value.length; if (size > limits.outputBytes) throw new Error('Plan exceeds output bounds'); chunks.push(value) }
 } finally { await reader.cancel().catch(() => {}); reader.releaseLock() }
 const raw = JSON.parse(Buffer.concat(chunks).toString('utf8'))
 if (!Array.isArray(raw) || raw.length !== 1 || !raw[0].Plan || Object.hasOwn(raw[0], 'Execution Time')) throw new Error('Expected an estimated planner document')
 return raw[0].Plan
}
export async function explainRequests(context, { url, identities, metadata, schemaFingerprint, signal }) {
 const baseline = context.baseline
 if (baseline && (baseline.dataset_fingerprint !== context.dataset_fingerprint || baseline.workload_fingerprint !== context.workload_fingerprint)) throw new Error('Plan baseline uses different fixture/seed, identities or saved requests; compare the same workload')
 const results = []
 for (const scenario of context.scenarios) for (const request of scenario.requests) {
  signal.throwIfAborted()
  const query = readQuery(request, metadata), base = { scenario: scenario.name, name: request.name, identity: request.identity }
  if (query.reason) { results.push({ ...base, status: 'skipped', reason: query.reason }); continue }
  const queryFingerprint = hash(JSON.stringify([request.method, request.path, request.identity, request.expectedSchemaFingerprint ?? null]))
  try {
   const target = new URL(request.path, url)
   if (request.expectedSchemaFingerprint && request.expectedSchemaFingerprint !== schemaFingerprint) throw new Error('Saved contract is stale')
   const plan = redactPlan(await planDocument(target, identities[request.identity].token, signal), metadata)
   const advice = planAdvice(plan, query, metadata)
   const before = baseline?.results.find(result => result.scenario === scenario.name && result.name === request.name && result.query_fingerprint === queryFingerprint && result.status === 'planned')
   results.push({ ...base, status: 'planned', query_fingerprint: queryFingerprint, relation: `api.${query.name}`, plan, metrics: { estimated_scan_rows: walk(plan).reduce((sum, node) => sum + (node.node_type === "Seq Scan" && node.relation ? metadata.tables.get(node.relation.slice(4)).rows : ["Index Scan", "Index Only Scan", "Bitmap Heap Scan", "Tid Scan", "Tid Range Scan", "Foreign Scan"].includes(node.node_type) ? node.estimated_rows : 0), 0) }, ...advice, ...(before ? { comparison: { baseline_schema_fingerprint: baseline.schema_fingerprint, before_cost: before.plan.total_cost, after_cost: plan.total_cost, cost_delta: plan.total_cost - before.plan.total_cost, ...(before.plan.total_cost > 0 ? { cost_ratio: plan.total_cost / before.plan.total_cost } : {}) } } : {}) })
  } catch { signal.throwIfAborted(); results.push({ ...base, status: 'failed', query_fingerprint: queryFingerprint, error: 'Could not plan the saved read; check its contract, fields, filters and local API permissions' }) }
 }
 return results
}
export function printExplain(report) {
 console.log(`Data API explain: ${report.status}; planner estimates, ANALYZE execution disabled`)
 for (const result of report.results) {
  console.log(`${result.status}: ${JSON.stringify(result.scenario)}/${JSON.stringify(result.name)}`)
  for (const finding of result.findings ?? []) console.log(`  ${finding.code}: ${finding.relation ?? ''} (${finding.severity})`)
  for (const suggestion of result.suggestions ?? []) console.log(`  Candidate: ${suggestion.sql}`)
  if (result.comparison) console.log(`  Estimated cost: ${result.comparison.before_cost} -> ${result.comparison.after_cost}`)
 }
 if (report.budget_check) {
  console.log(`Query budgets: ${report.budget_check.status}`)
  for (const violation of report.budget_check.violations) console.log(`  ${JSON.stringify(violation.scenario)}/${JSON.stringify(violation.name)}: ${violation.code}${violation.metric ? ` (${violation.metric})` : ''}`)
 }
 console.log(`Cleanup: ${report.cleanup}`)
}

// Budgets use planner units and approximate scan rows, never elapsed latency.
export function queryBudgets(value) {
 const object = item => item && typeof item === 'object' && !Array.isArray(item)
 const fields = ['max_total_cost', 'max_cost_ratio', 'max_scanned_rows']
 if (!object(value) || Object.keys(value).some(key => !['version', 'queries'].includes(key)) || value.version !== 1 || !Array.isArray(value.queries) || !value.queries.length || value.queries.length > 100) throw new Error('Invalid query budgets')
 const keys = new Set()
 for (const item of value.queries) {
  if (!object(item) || Object.keys(item).some(key => !['scenario', 'name', ...fields].includes(key)) || !['scenario', 'name'].every(key => typeof item[key] === 'string' && item[key].length > 0 && item[key].length <= 200) || !fields.some(key => Object.hasOwn(item, key)) || fields.some(key => Object.hasOwn(item, key) && (!numeric(item[key]) || key === 'max_cost_ratio' && item[key] === 0))) throw new Error('Invalid query budget')
  const key = JSON.stringify([item.scenario, item.name])
  if (keys.has(key)) throw new Error('Duplicate query budget')
  keys.add(key)
 }
 return value
}
export function checkQueryBudgets(budget, results) {
 const violations = []
 const key = item => JSON.stringify([item.scenario, item.name])
 const declared = new Map(budget.queries.map(item => [key(item), item]))
 for (const result of results.filter(item => item.status === 'planned')) {
  if (!declared.has(key(result))) violations.push({ scenario: result.scenario, name: result.name, code: 'missing_budget' })
 }
 for (const item of budget.queries) {
  const base = { scenario: item.scenario, name: item.name }
  const result = results.find(result => key(result) === key(item))
  if (!result || result.status !== 'planned') { violations.push({ ...base, code: 'query_not_planned' }); continue }
  if (!result.comparison) { violations.push({ ...base, code: 'missing_baseline_query' }); continue }
  const metrics = { max_total_cost: result.plan.total_cost, max_scanned_rows: result.metrics?.estimated_scan_rows }
  // Compare by multiplication so a zero baseline cannot yield Infinity or pass silently.
  if (Object.hasOwn(item, 'max_cost_ratio') && result.plan.total_cost > result.comparison.before_cost * item.max_cost_ratio) violations.push({ ...base, code: 'cost_ratio_exceeded', before_cost: result.comparison.before_cost, actual_cost: result.plan.total_cost, limit: item.max_cost_ratio })
  for (const [field, actual] of Object.entries(metrics)) if (Object.hasOwn(item, field)) {
   if (!numeric(actual)) violations.push({ ...base, code: 'metric_unavailable', metric: field })
   else if (actual > item[field]) violations.push({ ...base, code: 'budget_exceeded', metric: field, actual, limit: item[field] })
  }
 }
 return { status: violations.length ? 'failed' : 'passed', checked: budget.queries.length, violations }
}
