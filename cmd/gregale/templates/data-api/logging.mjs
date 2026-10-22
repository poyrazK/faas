import { randomUUID, createHash } from 'node:crypto'
import { createTimings, timingPhases } from './timing.mjs'

const trace = Symbol('requestTrace')
const codes = new Set(['origin_not_allowed', 'not_found', 'invalid_path', 'method_not_allowed',
  'token_required', 'token_invalid', 'read_only_identity', 'serving_contract_unavailable', 'data_api_unavailable',
  'data_api_refresh_paused', 'data_api_refresh_unavailable', 'data_api_overloaded', 'data_api_rate_limited', 'data_api_concurrency_limited',
  'request_too_large', 'response_too_large', 'query_timeout', 'request_deadline_exceeded', 'schema_document_too_large', 'schema_contract_mismatch', 'upstream_error',
  'public_read_not_allowed', 'public_rpc_requires_get', 'server_key_browser_forbidden', 'server_key_permission_denied', 'data_api_monthly_cap_reached'])

function route(url) {
  if (url === '/healthz' || url === '/livez') return 'health'
  if (url === '/__gregale/schema') return 'schema'
  if (url === '/openapi.json') return 'openapi'
  if (url.startsWith('/rest/v1/rpc/')) return 'rpc'
  if (url.startsWith('/rest/v1/')) return 'rest'
  return 'unknown'
}

function outcome(code, status) {
  if (code === 'token_required' || code === 'token_invalid') return 'authentication_failed'
  if (code === 'query_timeout' || code === 'request_deadline_exceeded') return 'query_timeout'
  if (code === 'data_api_overloaded') return 'overloaded'
  if (code === 'data_api_unavailable' || code === 'data_api_refresh_unavailable' || code === 'serving_contract_unavailable') return 'unavailable'
  if (code === 'upstream_error') return 'upstream_error'
  return status >= 400 ? 'rejected' : 'completed'
}

function writeLog(record) { process.stderr.write(JSON.stringify(record) + '\n') }

export function traceRequest(req, res, emit = writeLog) {
  const started = performance.now()
  const state = { id: randomUUID(), code: null }
  res[trace] = state
  res.setHeader('X-Request-Id', state.id)
  const method = ['GET', 'HEAD', 'POST', 'PATCH', 'DELETE', 'OPTIONS', 'PUT'].includes(req.method) ? req.method : 'OTHER'
  const category = route(req.url)
  let emitted = false
  const finish = completed => {
    if (emitted) return
    emitted = true
    state.closed = true
    const finishedAt = performance.now()
    const interrupted = !completed || state.code === 'query_timeout' || state.code === 'request_deadline_exceeded' || state.code === 'response_too_large'
    const timings = state.timing?.snapshot(finishedAt, !interrupted)
    const status = completed || res.headersSent ? res.statusCode : null
    const result = completed ? outcome(state.code, status) : 'aborted'
    const record = { time: new Date().toISOString(), level: completed && status < 400 ? 'info' : 'warn',
      event: 'data_api_request', request_id: state.id, method, route: category, status,
      duration_ms: Math.round((finishedAt - started) * 1000) / 1000, outcome: result, code: state.code, ...(state.access ? { access: state.access } : {}), ...(state.admission ? { admission: state.admission } : {}), ...(state.deadline ? { deadline: state.deadline } : {}), ...(state.responseLimit ? { response_limit: state.responseLimit } : {}), ...(state.rateLimit ? { rate_limit: state.rateLimit } : {}), ...(state.concurrencyLimit ? { concurrency_limit: state.concurrencyLimit } : {}), ...(timings ? { timings } : {}), ...(state.databaseTimings ? { database_timings: state.databaseTimings } : {}), ...(state.query ?? {}) }
    try { emit(record) } catch { /* Logging failure must not change the response. */ }
  }
  res.once('finish', () => finish(true))
  res.once('close', () => finish(false))
}

export function markRequest(res, code) {
  if (res[trace] && codes.has(code)) res[trace].code = code
}

// Access kind and server-key slot only: never subjects, client addresses or keys.
export function markAccess(res, access) {
  if (res[trace] && ['user', 'anonymous', 'server'].includes(access?.kind)) res[trace].access = { kind: access.kind, ...(['a', 'b'].includes(access.server_key_slot) ? { server_key_slot: access.server_key_slot, ...(access.server_key_restricted === true ? { server_key_restricted: true } : {}) } : {}) }
}

export function markAdmission(res, admission) {
  if (res[trace]) res[trace].admission = admission
}

export function markDeadline(res, deadline) {
  if (res[trace]) res[trace].deadline = deadline
}

export function markResponseLimit(res, limit) {
  if (res[trace] && !res[trace].closed) res[trace].responseLimit = limit
}

export function markRateLimit(res, limit) {
  if (res[trace] && !res[trace].closed) res[trace].rateLimit = limit
}

export function markConcurrencyLimit(res, limit) {
  if (res[trace] && !res[trace].closed) res[trace].concurrencyLimit = limit
}

export function markPhase(res, phase) {
  const state = res[trace]
  if (!state || state.closed || !timingPhases.includes(phase)) return
  state.timing ??= createTimings()
  state.timing.enter(phase, performance.now())
}

export function markDatabaseTimings(res, timings) {
  if (res[trace] && !res[trace].closed) res[trace].databaseTimings = timings
}

export function requestID(res) { return res[trace]?.id }

export function runtimeFailure(event) {
  const allowed = ['data_api_configuration_invalid', 'data_api_postgrest_start_failed', 'data_api_public_read_unsafe', 'data_api_public_rpc_unsafe', 'data_api_storage_unsafe', 'data_api_change_events_unavailable', 'data_api_change_event_rejected', 'data_api_change_events_unsettled']
  try { writeLog({ time: new Date().toISOString(), level: 'error', event: allowed.includes(event) ? event : 'data_api_runtime_failed' }) } catch {}
}

const identifier = /^[A-Za-z_][A-Za-z0-9_]{0,62}$/
const operators = new Set(['eq','neq','gt','gte','lt','lte','like','ilike','in','is','cs','cd','ov','sl','sr','nxl','nxr','adj','fts','plfts','phfts','wfts'])
// Only authenticated, catalog-recognized operations may contribute query metadata.
export function markQueryShape(req, res, config) {
 if (!config.queryShapes || !res[trace] || req.url.length > 4096 || !/^[a-f0-9]{64}$/.test(config.fingerprint ?? '')) return
 try {
  const url = new URL(req.url, 'http://127.0.0.1')
  const rpc = url.pathname.startsWith('/rest/v1/rpc/')
  const name = decodeURIComponent(url.pathname.slice(rpc ? '/rest/v1/rpc/'.length : '/rest/v1/'.length))
  const schema = req.headers[['GET','HEAD'].includes(req.method) ? 'accept-profile' : 'content-profile'] ?? config.schemas[0]
  if (!identifier.test(name) || !identifier.test(schema)) return
  const catalog = rpc ? config.functions?.find(item => item.schema === schema && item.name === name) : config.snapshot?.tables.find(item => item.schema === schema && item.name === name)
  if (!catalog) return
  const columns = new Set((rpc ? catalog.args : catalog.columns).map(column => column.name).filter(name => identifier.test(name)))
  const shape = { projection: [], filters: [], order: [], complex: false, limit: false, offset: false, count: 'none' }
  let parameters = 0
  for (const [key,value] of url.searchParams) {
   if (++parameters > 64) return
   if (key === 'select') {
    for (const column of value.split(',')) { if (column === '*' || columns.has(column)) shape.projection.push(column); else shape.complex = true }
   } else if (key === 'order') {
    for (const item of value.split(',')) {
     const [column,direction='asc',nullsValue,...extra] = item.split('.')
     const nulls = nullsValue ?? (direction === 'asc' ? 'nullslast' : 'nullsfirst')
     if (columns.has(column) && ['asc','desc'].includes(direction) && ['nullsfirst','nullslast'].includes(nulls) && !extra.length) shape.order.push({column,ascending:direction==='asc',nulls_first:nulls==='nullsfirst'})
     else shape.complex = true
    }
   } else if (key === 'limit') shape.limit = true
   else if (key === 'offset') shape.offset = true
   else if (columns.has(key)) {
    const parts = value.split('.'), negated = parts[0] === 'not', operator = parts[negated ? 1 : 0]
    shape.filters.push({column:key,operator:operators.has(operator) ? (negated ? 'not.' : '')+operator : 'unknown'})
   } else shape.complex = true
  }
  if (!shape.projection.length) shape.projection = ['*']
  shape.projection = [...new Set(shape.projection)].sort()
  shape.filters.sort((a,b) => a.column.localeCompare(b.column) || a.operator.localeCompare(b.operator))
  const prefer = typeof req.headers.prefer === 'string' ? req.headers.prefer : ''
  const count = /(?:^|,)\s*count=(exact|planned|estimated)(?:\s*,|$)/.exec(prefer)
  if (count) shape.count = count[1]
  if (shape.projection.length > 64 || shape.filters.length > 64 || shape.order.length > 16) return
  const operation = `${schema}.${name}`, operation_kind = rpc ? 'rpc' : 'relation'
  const query_shape_id = createHash('sha256').update(JSON.stringify([req.method,operation,operation_kind,shape])).digest('hex')
  res[trace].query = { operation, operation_kind, query_shape_id, query_shape: shape, schema_fingerprint: config.fingerprint }
 } catch { /* Invalid/complex caller input must never disrupt logging or a response. */ }
}
