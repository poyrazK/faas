import http from 'node:http'
import { refreshStatusReader, refreshTokenHash } from './refresh.mjs'
import { spawn, execFile } from 'node:child_process'
import { promisify } from 'node:util'
import { pathToFileURL } from 'node:url'
import { createRemoteJWKSet, jwtVerify, SignJWT } from 'jose'
import { traceRequest, markRequest, markAccess, markQueryShape, markDatabaseTimings, requestID, runtimeFailure } from './logging.mjs'
import { parseDatabaseTimings, unknownDatabaseTimings } from './database-timing.mjs'
import { inspect, fingerprint, enrichOpenAPI } from './types.mjs'
import { runtimeConfig, servingContracts, limits } from './config.mjs'
import { createAdmission } from './admission.mjs'
import { createHealth } from './health.mjs'
import { createDeadline } from './deadline.mjs'
import { checkSQLDeadlineConnection } from './sql-deadline.mjs'
import { streamResponse } from './response-limit.mjs'
import { createRateLimiter, admitRateLimit } from './rate-limit.mjs'
import { createUserConcurrency, admitUserConcurrency } from './user-concurrency.mjs'
import { qualifiedConnectionEngine, startConnectionReporter } from './connection-observations.mjs'
import { createPoolObservation } from './pool.mjs'
import { createUsage, addResponseBytes, accessReport } from './usage.mjs'
import { publicReadTarget, publicRpcTarget, publicRpcListed, publicReadCache, clientKey, matchServerKey, serverKeyAllows, serverSubject, checkPublicRelations, checkPublicFunctions } from './access.mjs'

export function createServer(config, verify, upstreamPort = 3000, readyPort = 3001, log) {
  const servingFingerprint = config.fingerprint
  const acceptedFingerprints = servingContracts(config)
  const health = createHealth(config, upstreamPort, readyPort)
  const rateLimiter = createRateLimiter(config.rateLimit)
  // Anonymous clients always have a budget and never use subject tracking slots.
  const anonymousLimiter = config.publicRead || config.publicRpc ? createRateLimiter(config.rateLimit ?? { perSecond: limits.rateLimitDefaultPerSecond, burst: limits.rateLimitDefaultBurst }) : null
  const userConcurrency = createUserConcurrency(config.userConcurrency, config.coordinatedRefresh ? limits.pool - 1 : limits.pool)
  const admitted = createAdmission(config.coordinatedRefresh ? limits.pool - 1 : limits.pool, res => {
    res.setHeader('Retry-After', String(limits.overloadRetrySeconds))
    problem(res, 503, 'data_api_overloaded')
  })
  const usage = createUsage(config.usage, { access: accessReport(config) })
  const server = http.createServer(async (req, res) => {
    const guarded = /^\/__gregale\/contract\/([a-f0-9]{64})(\/rest\/v1\/.*)$/.exec(req.url)
    if (guarded) req.url = guarded[2]
    traceRequest(req, res, log)
    const origin = req.headers.origin
    if (config.publicReadMaxAge && req.headers.authorization === undefined && req.method !== 'OPTIONS') {
      // Edge-cached public reads are shared across origins, so they cannot vary
      // on Origin. They carry no credentials; any origin may read public rows.
      res.setHeader('Access-Control-Allow-Origin', '*')
      res.setHeader('Access-Control-Expose-Headers', 'Content-Range,Preference-Applied,X-Request-Id,Retry-After')
    } else if (origin && config.origins.includes(origin)) {
      res.setHeader('Access-Control-Allow-Origin', origin)
      res.setHeader('Vary', 'Origin')
      res.setHeader('Access-Control-Expose-Headers', 'Content-Range,Preference-Applied,X-Request-Id,Retry-After')
    }
    if (req.method === 'OPTIONS') {
      if (!origin || !config.origins.includes(origin)) return problem(res, 403, 'origin_not_allowed')
      res.writeHead(204, { 'Access-Control-Allow-Methods': 'GET,HEAD,POST,PATCH,DELETE,OPTIONS', 'Access-Control-Allow-Headers': 'Authorization,Content-Type,Prefer,Range,Range-Unit,Accept-Profile,Content-Profile', 'Access-Control-Max-Age': '600' })
      return res.end()
    }
    if (req.url === '/livez' && req.method === 'GET') return healthResponse(res, 200, { version: 1, live: true })
    if (req.url === '/healthz' && req.method === 'GET') {
      const report = await health.snapshot(admitted.snapshot)
      if (!report.ready) markRequest(res, report.checks.refresh === 'unavailable' ? 'data_api_refresh_unavailable' : 'data_api_unavailable')
      return healthResponse(res, report.ready ? 200 : 503, report)
    }
    // Health checks and preflights are not usage.
    const counted = usage?.start(res)
    if (!req.url.startsWith('/rest/v1/') && req.url !== '/openapi.json' && req.url !== '/__gregale/schema') return problem(res, 404, 'not_found')
    let rpc
    if (req.url.startsWith('/rest/v1/')) {
      const raw = req.url.slice('/rest/v1/'.length).split('?')[0]
      let path
      try { path = decodeURIComponent(raw) } catch { return problem(res, 400, 'invalid_path') }
      if (path.includes('/')) {
        const match = /^rpc\/([a-z][a-z0-9_]{0,62})$/.exec(path)
        const read = ['GET', 'HEAD'].includes(req.method)
        const schema = req.headers[read ? 'accept-profile' : 'content-profile'] ?? config.schemas[0]
        if (raw !== path || !match) return problem(res, 404, 'not_found')
        rpc = { schema, name: match[1] }
        if (!guarded && !config.functions?.some(f => f.schema === rpc.schema && f.name === rpc.name)) return problem(res, 404, 'not_found')
        // Only public functions take GET, which PostgREST runs read-only.
        if (req.method !== 'POST' && !(read && publicRpcListed(config, rpc))) return problem(res, 405, 'method_not_allowed')
      }
    }
    if (!['GET', 'HEAD', 'POST', 'PATCH', 'DELETE'].includes(req.method)) return problem(res, 405, 'method_not_allowed')
    if (['/__gregale/schema', '/openapi.json'].includes(req.url) && req.method !== 'GET') return problem(res, 405, 'method_not_allowed')
    const token = req.headers.authorization?.match(/^Bearer ([^\s]+)$/i)?.[1]
    let access = 'user', serverKeySlot = null, serverKeyPermissions = null, subject = null, publicCache = null
    if (req.headers.authorization === undefined) {
      // Never downgrade a present but invalid credential to anonymous access.
      const target = rpc ? publicRpcTarget(req, config, rpc) : publicReadTarget(req, config)
      if (target.code === 'public_rpc_requires_get') res.setHeader('Allow', 'GET, HEAD')
      if (target.code) return problem(res, { public_read_not_allowed: 403, public_rpc_requires_get: 405 }[target.code] ?? 401, target.code)
      access = 'anonymous'
      publicCache = publicReadCache(req, config, target)
    } else if (!token) return problem(res, 401, 'token_required')
    else if (token.startsWith('gdsk_')) {
      access = 'server'
      if (req.headers.origin !== undefined) return problem(res, 403, 'server_key_browser_forbidden')
      serverKeySlot = matchServerKey(config.serverKeys, token)
      const named = serverSubject(req.headers['x-gregale-subject'])
      if (!serverKeySlot || named.invalid) return problem(res, 401, 'token_invalid')
      subject = named.subject
      serverKeyPermissions = config.serverKeys.find(entry => entry.slot === serverKeySlot).permissions
    }
    markAccess(res, { kind: access, ...(serverKeySlot ? { server_key_slot: serverKeySlot, ...(serverKeyPermissions ? { server_key_restricted: true } : {}) } : {}) })
    if (counted) counted.access = access
    if (serverKeyPermissions && !serverKeyAllows(req, serverKeyPermissions, config, rpc)) return problem(res, 403, 'server_key_permission_denied')
    if (access === 'server' && config.readOnly === true && !['GET', 'HEAD'].includes(req.method)) return problem(res, 403, 'read_only_identity')
    // Owner caps are checked before admission, against month-to-date totals.
    const cap = usage?.capReached(access)
    if (cap) {
      counted.capped = true
      res.setHeader('Retry-After', String(cap.retryAfter))
      return problem(res, 429, 'data_api_monthly_cap_reached', { cap: cap.name, limit: cap.limit, observed: cap.observed, docs: 'https://gregale.dev/docs/data-api#usage-and-monthly-caps' })
    }
    const declared = Number(req.headers['content-length'] ?? 0)
    if (!Number.isSafeInteger(declared) || declared < 0 || declared > limits.bodyBytes) return problem(res, 413, 'request_too_large')
    return admitted(req, res, async () => {
      const deadline = createDeadline(req, res, () => {
        if (!res.headersSent) res.setHeader('Connection', 'close')
        problem(res, 504, 'request_deadline_exceeded')
      })
      let userLease
      try {
        let payload, concurrencyKey = null
        if (access === 'user') {
          try { payload = await verify(token) } catch {
            if (deadline.check()) problem(res, 401, 'token_invalid')
            return
          }
          if (!deadline.check()) return
          if (typeof payload.sub !== 'string' || !payload.sub || typeof payload.exp !== 'number') return problem(res, 401, 'token_invalid')
          if (payload.gregale_read_only !== undefined && typeof payload.gregale_read_only !== 'boolean') return problem(res, 401, 'token_invalid')
          if (!admitRateLimit(rateLimiter, payload.sub, res, problem)) return
          if ((config.readOnly === true || payload.gregale_read_only === true) && !['GET','HEAD'].includes(req.method)) return problem(res, 403, 'read_only_identity')
          concurrencyKey = payload.sub
        } else if (access === 'anonymous') {
          const client = ['anonymous', clientKey(req)]
          if (!admitRateLimit(anonymousLimiter, client, res, problem)) return
          concurrencyKey = client
        }
        if (guarded) {
          if (!/^[a-f0-9]{64}$/.test(servingFingerprint ?? '')) return problem(res, 503, 'serving_contract_unavailable')
          if (guarded[1] !== servingFingerprint && !acceptedFingerprints.has(guarded[1])) return problem(res, 409, 'schema_contract_mismatch')
          if (rpc && !config.functions?.some(f => f.schema === rpc.schema && f.name === rpc.name)) return problem(res, 404, 'not_found')
        }
        // Trusted server keys skip per-user budgets, never HTTP admission.
        if (concurrencyKey !== null) {
          userLease = admitUserConcurrency(userConcurrency, concurrencyKey, req, res, problem)
          if (userLease === false) return
        }
        if (req.url.startsWith('/rest/v1/')) markQueryShape(req, res, config)
        if (req.url === '/__gregale/schema') {
          if (!/^[a-f0-9]{64}$/.test(servingFingerprint ?? '')) return problem(res, 503, 'serving_contract_unavailable')
          try {
            if (!deadline.enter('refresh')) return
            if (config.refreshStatus) userLease?.dispatched()
            const gate = config.refreshStatus ? await config.refreshStatus(req.headers['x-gregale-refresh-token']) : null
            userLease?.completed()
            if (!deadline.enter('readiness')) return
            return await readiness(res, health, { version: 1, fingerprint: servingFingerprint, ...(gate ? { refresh_protocol: 1, refresh_paused: gate.paused, refresh_verified: gate.owns_pause === true, refresh_fingerprint: gate.fingerprint } : {}) }, deadline, userLease)
          } catch { return problem(res, 503, 'data_api_refresh_unavailable') }
        }
        if (!await admitRefreshRequest(config, req, res, deadline, userLease)) return
        if (!deadline.enter('signing')) return
        // Only the binding login may become the SQL role. External role claims
        // cannot select an administrator, owner or another application's role.
        const expires = Math.ceil(Date.now() / 1000) + Math.ceil(limits.queryMs / 1000) + 5
        const claims = access === 'user' ? { ...payload, role: config.role } : { role: config.role, exp: expires, ...(subject !== null ? { sub: subject } : {}) }
        // Access kinds and the server-key slot are gateway-owned claims.
        delete claims.gregale_server_key
        claims.gregale_access = access
        if (serverKeySlot) claims.gregale_server_key = serverKeySlot
        // A caller cannot extend or enable the internal SQL retry deadline.
        delete claims.gregale_sql_deadline
        if (config.sqlDeadline) claims.gregale_sql_deadline = { version: 1, expires_at_ms: deadline.expiresAt, coordinated_refresh: config.coordinatedRefresh === true }
        // Never accept reserved coordination claims from an application JWT.
        if (config.coordinatedRefresh) {
          delete claims.gregale_refresh_hash
          delete claims.gregale_serving_fingerprint
          claims.gregale_serving_fingerprint = servingFingerprint
          const refreshHash = refreshTokenHash(req.headers['x-gregale-refresh-token'])
          if (refreshHash) claims.gregale_refresh_hash = refreshHash
        }
        const internal = await new SignJWT(claims).setProtectedHeader({ alg: 'HS256' }).sign(new TextEncoder().encode(config.secret.toString('base64url')))
        if (!deadline.check()) return
        proxy(req, res, internal, upstreamPort, config, guarded ? `/__gregale/contract/${guarded[1]}/rest/v1` : '/rest/v1', deadline, userLease, guarded ? null : publicCache)
      } catch { problem(res, 503, 'data_api_unavailable') }
      finally { if (userLease) userLease.settled() }
    })
  })
  server.usage = usage
  server.on('close', () => usage?.stop())
  return server
}

// A deliberate pause is an admission decision, not an engine failure.
// PostgREST recovers its pool/schema cache for HTTP 503 responses, so block
// known paused traffic here. Its SQL pre-request hook still fences races.
async function admitRefreshRequest(config, req, res, deadline, userLease) {
  if (!config.coordinatedRefresh) return true
  try {
    if (!deadline.enter('refresh')) return false
    if (typeof config.refreshStatus !== 'function') throw new Error('Refresh reader unavailable')
    userLease?.dispatched()
    const gate = await config.refreshStatus(req.headers['x-gregale-refresh-token'])
    userLease?.completed()
    if (!deadline.check()) return false
    if (!gate || typeof gate.paused !== 'boolean' || !/^[a-f0-9]{64}$/.test(gate.fingerprint ?? '')) throw new Error('Invalid refresh state')
    if (gate.paused && gate.owns_pause !== true) {
      problem(res, 503, 'data_api_refresh_paused')
      return false
    }
    return true
  } catch {
    problem(res, 503, 'data_api_refresh_unavailable')
    return false
  }
}

function problem(res, status, code, extra = {}) {
  if (res.destroyed || res.writableEnded) return
  markRequest(res, code)
  if (res.headersSent) { res.destroy(); return }
  res.writeHead(status, { 'Content-Type': 'application/problem+json', 'Cache-Control': 'no-store' })
  res.end(JSON.stringify({ type: 'about:blank', title: code, status, code, request_id: requestID(res), ...extra }))
}

function healthResponse(res, status, body) {
  if (res.destroyed || res.writableEnded) return
  res.writeHead(status, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' })
  res.end(JSON.stringify(body))
}

async function readiness(res, health, contract = {}, deadline, userLease) {
  userLease?.dispatched()
  const sql = await health.sql()
  if (sql !== 'unknown') userLease?.completed()
  const ready = sql === 'ready'
  if (deadline && !deadline.check()) return
  if (!ready) markRequest(res, 'data_api_unavailable')
  return healthResponse(res, ready ? 200 : 503, { ready, ...(ready ? contract : {}) })
}

function proxy(req, res, token, port, config, restBasePath, deadline, userLease, publicCache = null) {
  const declared = Number(req.headers['content-length'] ?? 0)
  if (!Number.isSafeInteger(declared) || declared < 0 || declared > limits.bodyBytes) return problem(res, 413, 'request_too_large')
  const headers = { authorization: `Bearer ${token}` }
  // Forward only PostgREST's data contract, never client-supplied internal
  // identity, proxy credentials or hop-by-hop headers.
  for (const name of ['accept', 'content-type', 'prefer', 'range', 'range-unit', 'accept-profile', 'content-profile']) if (req.headers[name]) headers[name] = req.headers[name]
  const path = req.url === '/openapi.json' ? '/' : req.url.slice('/rest/v1'.length)
  if (!deadline.enter('upstream') || !deadline.dispatch()) return
  userLease?.dispatched()
  markDatabaseTimings(res, unknownDatabaseTimings('no_response'))
  // The total deadline owns expiry and aborts this request. A second socket
  // timer can fire early and preempt its stable response and deadline evidence.
  const upstream = http.request({ hostname: '127.0.0.1', port, path, method: req.method, headers, signal: deadline.signal }, response => {
    userLease?.completed()
    if (!deadline.enter('streaming')) { response.destroy(); return }
    markDatabaseTimings(res, parseDatabaseTimings(response.headers['server-timing']))
    response.on('data', () => deadline.check())
    if (req.method === 'GET' && path.split('?')[0] === '/' && response.statusCode === 200) return openAPISpec(response, res, config, restBasePath, deadline)
    if (response.statusCode >= 400) markRequest(res, 'upstream_error')
    streamResponse(response, res, req.method, deadline, problem, publicCache)
  })
  let bytes = 0
  req.on('data', chunk => {
    bytes += chunk.length
    if (bytes > limits.bodyBytes) { problem(res, 413, 'request_too_large'); upstream.destroy() }
  })
  req.on('aborted', () => upstream.destroy())
  res.on('close', () => { if (!res.writableEnded) upstream.destroy() })
  upstream.on('error', () => { if (deadline.check()) problem(res, 503, 'data_api_unavailable') })
  req.pipe(upstream)
}

function openAPISpec(response, res, config, restBasePath, deadline) {
  let bytes = 0
  const chunks = []
  response.on('data', chunk => {
    if (!deadline.check()) return
    bytes += chunk.length
    if (bytes > limits.outputBytes) { problem(res, 503, 'schema_document_too_large'); response.destroy(); return }
    chunks.push(chunk)
  })
  response.on('error', () => { if (!res.writableEnded) problem(res, 503, 'data_api_unavailable') })
  response.on('end', () => {
    if (!deadline.check()) return
    try {
      const document = JSON.parse(Buffer.concat(chunks).toString('utf8'))
      // Swagger 2 defaults an omitted host/scheme to the document's origin.
      // The engine's loopback address must not become the public client URL.
      delete document.host
      delete document.schemes
      document.basePath = restBasePath
      const schema = response.req.getHeader('accept-profile') ?? config.schemas[0]
      for (const path of Object.keys(document.paths ?? {})) if (path.startsWith('/rpc/')) {
        if (!config.functions?.some(f => f.schema === schema && f.name === path.slice(5))) delete document.paths[path]
        else for (const method of Object.keys(document.paths[path])) if (method !== 'post' && method !== 'parameters' && !(method === 'get' && publicRpcListed(config, { schema, name: path.slice(5) }))) delete document.paths[path][method]
      }
      const output = JSON.stringify(enrichOpenAPI(document, config.snapshot, schema))
      if (Buffer.byteLength(output) > limits.outputBytes) return problem(res, 503, 'schema_document_too_large')
      if (!deadline.check()) return
      res.writeHead(200, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' })
      addResponseBytes(res, Buffer.byteLength(output))
      res.end(output)
    } catch { problem(res, 503, 'data_api_unavailable') }
  })
}

export async function main(env = process.env) {
  const config = runtimeConfig(env)
  if (config.connectionObservation || config.sqlDeadline) {
    const { stdout } = await promisify(execFile)(env.DATA_API_POSTGREST_BIN || '/usr/local/bin/postgrest', ['--version'], { timeout: limits.connectionReportMs, maxBuffer: limits.healthBytes })
    if (!qualifiedConnectionEngine(stdout)) throw new Error('Unsupported connection observation engine')
  }
  if (config.sqlDeadline) await checkSQLDeadlineConnection(config.connection, { coordinatedRefresh: config.coordinatedRefresh })
  if (config.publicRead) {
    try { await checkPublicRelations(config.connection, config.publicRead) } catch (error) {
      runtimeFailure('data_api_public_read_unsafe')
      throw Object.assign(new Error('Unsafe public read configuration'), { cause: error, reported: true })
    }
  }
  const snapshot = await inspect(config.connection, config.schemas)
  config.snapshot = snapshot
  config.functions = snapshot.functions
  if (config.publicRpc) {
    try { await checkPublicFunctions(config.connection, config.publicRpc, snapshot.functions) } catch (error) {
      runtimeFailure('data_api_public_rpc_unsafe')
      throw Object.assign(new Error('Unsafe public RPC configuration'), { cause: error, reported: true })
    }
  }
  config.fingerprint = fingerprint(snapshot)
  // Reject stale owner policies before starting an engine process.
  servingContracts(config)
  const refresh = config.coordinatedRefresh ? refreshStatusReader(config.connection) : null
  if (refresh) {
    let gate
    try { gate = await refresh.read() } catch (error) { await refresh.close(); throw error }
    if (!gate.paused && gate.fingerprint && gate.fingerprint !== config.fingerprint) { await refresh.close(); throw new Error('Serving refresh contract changed') }
    config.refreshStatus = refresh.read
  }
  const jwks = createRemoteJWKSet(config.auth.jwks, { timeoutDuration: 3000, cacheMaxAge: 300000 })
  config.poolObservation = createPoolObservation(3001, config.coordinatedRefresh ? limits.pool - 1 : limits.pool)
  const verify = tokenVerifier(config.auth, jwks)
  // Avoid inheriting arbitrary PGRST_* overrides from application secrets.
  const childEnv = Object.fromEntries(Object.entries(env).filter(([k]) => !k.startsWith('PGRST_')))
  const child = spawn(env.DATA_API_POSTGREST_BIN || '/usr/local/bin/postgrest', [], { env: { ...childEnv, ...config.postgrestEnv }, stdio: ['ignore', 'ignore', 'ignore'] })
  const server = createServer(config, verify)
  const stopReporting = startConnectionReporter(config)
  server.on('close', stopReporting)
  server.on('close', () => { void refresh?.close() })
  let stopping = false
  server.requestTimeout = limits.queryMs + 5000
  server.headersTimeout = 10000
  child.on('error', () => { runtimeFailure('data_api_postgrest_start_failed'); process.exitCode = 1; server.close() })
  child.on('exit', code => { process.exitCode = stopping ? 0 : (code || 1); server.close() })
  // Report the last unreported usage before the process exits.
  for (const signal of ['SIGTERM', 'SIGINT']) process.once(signal, () => { stopping = true; void server.usage?.flush(); server.close(); child.kill(signal) })
  process.on('SIGUSR1', () => {
    if (config.coordinatedRefresh) runtimeFailure('data_api_uncoordinated_reload_rejected')
    else child.kill('SIGUSR1')
  })
  server.listen(Number(env.PORT || 8080), '0.0.0.0')
}

export function tokenVerifier(auth, keys) {
  return async token => (await jwtVerify(token, keys, { issuer: auth.issuer, audience: auth.audience, algorithms: ['RS256', 'ES256'], requiredClaims: ['sub', 'exp'] })).payload
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) main().catch(error => { if (error?.reported !== true) runtimeFailure('data_api_configuration_invalid'); process.exitCode = 1 })
