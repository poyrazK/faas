import { randomUUID } from 'node:crypto'

const trace = Symbol('requestTrace')
const codes = new Set(['origin_not_allowed', 'not_found', 'invalid_path', 'method_not_allowed',
  'token_required', 'token_invalid', 'serving_contract_unavailable', 'data_api_unavailable',
  'request_too_large', 'query_timeout', 'schema_document_too_large', 'upstream_error'])

function route(url) {
  if (url === '/healthz') return 'health'
  if (url === '/__gregale/schema') return 'schema'
  if (url === '/openapi.json') return 'openapi'
  if (url.startsWith('/rest/v1/rpc/')) return 'rpc'
  if (url.startsWith('/rest/v1/')) return 'rest'
  return 'unknown'
}

function outcome(code, status) {
  if (code === 'token_required' || code === 'token_invalid') return 'authentication_failed'
  if (code === 'query_timeout') return 'query_timeout'
  if (code === 'data_api_unavailable' || code === 'serving_contract_unavailable') return 'unavailable'
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
    const status = completed || res.headersSent ? res.statusCode : null
    const result = completed ? outcome(state.code, status) : 'aborted'
    const record = { time: new Date().toISOString(), level: completed && status < 400 ? 'info' : 'warn',
      event: 'data_api_request', request_id: state.id, method, route: category, status,
      duration_ms: Math.round((performance.now() - started) * 1000) / 1000, outcome: result, code: state.code }
    try { emit(record) } catch { /* Logging failure must not change the response. */ }
  }
  res.once('finish', () => finish(true))
  res.once('close', () => finish(false))
}

export function markRequest(res, code) {
  if (res[trace] && codes.has(code)) res[trace].code = code
}

export function requestID(res) { return res[trace]?.id }

export function runtimeFailure(event) {
  const allowed = ['data_api_configuration_invalid', 'data_api_postgrest_start_failed']
  try { writeLog({ time: new Date().toISOString(), level: 'error', event: allowed.includes(event) ? event : 'data_api_runtime_failed' }) } catch {}
}
