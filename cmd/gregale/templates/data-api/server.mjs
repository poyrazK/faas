import http from 'node:http'
import { spawn } from 'node:child_process'
import { pathToFileURL } from 'node:url'
import { createRemoteJWKSet, jwtVerify, SignJWT } from 'jose'
import { runtimeConfig, limits } from './config.mjs'

export function createServer(config, verify, upstreamPort = 3000, readyPort = 3001) {
  return http.createServer(async (req, res) => {
    const origin = req.headers.origin
    if (origin && config.origins.includes(origin)) {
      res.setHeader('Access-Control-Allow-Origin', origin)
      res.setHeader('Vary', 'Origin')
      res.setHeader('Access-Control-Expose-Headers', 'Content-Range,Preference-Applied')
    }
    if (req.method === 'OPTIONS') {
      if (!origin || !config.origins.includes(origin)) return problem(res, 403, 'origin_not_allowed')
      res.writeHead(204, { 'Access-Control-Allow-Methods': 'GET,HEAD,POST,PATCH,DELETE,OPTIONS', 'Access-Control-Allow-Headers': 'Authorization,Content-Type,Prefer,Range,Range-Unit,Accept-Profile,Content-Profile', 'Access-Control-Max-Age': '600' })
      return res.end()
    }
    if (req.url === '/healthz' && req.method === 'GET') return readiness(res, readyPort)
    if (!req.url.startsWith('/rest/v1/') && req.url !== '/openapi.json') return problem(res, 404, 'not_found')
    // V1 exposes only relation routes and OpenAPI. Decode before checking so
    // percent-encoded separators cannot enter PostgREST's RPC routes.
    if (req.url.startsWith('/rest/v1/')) {
      let relation
      try { relation = decodeURIComponent(req.url.slice('/rest/v1/'.length).split('?')[0]) } catch { return problem(res, 400, 'invalid_path') }
      if (relation.includes('/')) return problem(res, 404, 'not_found')
    }
    if (!['GET', 'HEAD', 'POST', 'PATCH', 'DELETE'].includes(req.method)) return problem(res, 405, 'method_not_allowed')
    const token = req.headers.authorization?.match(/^Bearer ([^\s]+)$/i)?.[1]
    if (!token) return problem(res, 401, 'token_required')
    let payload
    try { payload = await verify(token) } catch { return problem(res, 401, 'token_invalid') }
    if (typeof payload.sub !== 'string' || !payload.sub || typeof payload.exp !== 'number') return problem(res, 401, 'token_invalid')
    try {
      // Only the binding login may become the SQL role. External role claims
      // cannot select an administrator, owner or another application's role.
      const claims = { ...payload, role: config.role }
      const internal = await new SignJWT(claims).setProtectedHeader({ alg: 'HS256' }).sign(new TextEncoder().encode(config.secret.toString('base64url')))
      proxy(req, res, internal, upstreamPort)
    } catch { problem(res, 503, 'data_api_unavailable') }
  })
}

function problem(res, status, code) {
  if (res.headersSent) { res.destroy(); return }
  res.writeHead(status, { 'Content-Type': 'application/problem+json', 'Cache-Control': 'no-store' })
  res.end(JSON.stringify({ type: 'about:blank', title: code, status, code }))
}

function readiness(res, port) {
  const request = http.get({ hostname: '127.0.0.1', port, path: '/ready', timeout: 2000 }, upstream => {
    upstream.resume()
    res.writeHead(upstream.statusCode === 200 ? 200 : 503, { 'Content-Type': 'application/json' })
    res.end(JSON.stringify({ ready: upstream.statusCode === 200 }))
  })
  request.on('timeout', () => request.destroy())
  request.on('error', () => problem(res, 503, 'data_api_unavailable'))
}

function proxy(req, res, token, port) {
  const declared = Number(req.headers['content-length'] ?? 0)
  if (!Number.isSafeInteger(declared) || declared < 0 || declared > limits.bodyBytes) return problem(res, 413, 'request_too_large')
  const headers = { authorization: `Bearer ${token}` }
  // Forward only PostgREST's data contract, never client-supplied internal
  // identity, proxy credentials or hop-by-hop headers.
  for (const name of ['accept', 'content-type', 'prefer', 'range', 'range-unit', 'accept-profile', 'content-profile']) if (req.headers[name]) headers[name] = req.headers[name]
  const path = req.url === '/openapi.json' ? '/' : req.url.slice('/rest/v1'.length)
  const upstream = http.request({ hostname: '127.0.0.1', port, path, method: req.method, headers, timeout: limits.queryMs }, response => {
    if (req.method === 'GET' && path.split('?')[0] === '/' && response.statusCode === 200) return openAPISpec(response, res)
    const out = {}
    for (const name of ['content-type', 'content-range', 'preference-applied', 'location']) if (response.headers[name]) out[name] = response.headers[name]
    res.writeHead(response.statusCode, { ...out, 'Cache-Control': 'no-store' })
    response.pipe(res)
    response.on('error', () => res.destroy())
  })
  let bytes = 0
  req.on('data', chunk => {
    bytes += chunk.length
    if (bytes > limits.bodyBytes) { problem(res, 413, 'request_too_large'); upstream.destroy() }
  })
  req.on('aborted', () => upstream.destroy())
  res.on('close', () => { if (!res.writableEnded) upstream.destroy() })
  upstream.on('timeout', () => { problem(res, 504, 'query_timeout'); upstream.destroy() })
  upstream.on('error', () => { if (!res.writableEnded) problem(res, 503, 'data_api_unavailable') })
  req.pipe(upstream)
}

function openAPISpec(response, res) {
  let bytes = 0
  const chunks = []
  response.on('data', chunk => {
    bytes += chunk.length
    if (bytes > limits.outputBytes) { problem(res, 503, 'schema_document_too_large'); response.destroy(); return }
    chunks.push(chunk)
  })
  response.on('error', () => { if (!res.writableEnded) problem(res, 503, 'data_api_unavailable') })
  response.on('end', () => {
    if (res.writableEnded) return
    try {
      const document = JSON.parse(Buffer.concat(chunks).toString('utf8'))
      for (const path of Object.keys(document.paths ?? {})) if (path.startsWith('/rpc/')) delete document.paths[path]
      res.writeHead(200, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' })
      res.end(JSON.stringify(document))
    } catch { problem(res, 503, 'data_api_unavailable') }
  })
}

export async function main(env = process.env) {
  const config = runtimeConfig(env)
  const jwks = createRemoteJWKSet(config.auth.jwks, { timeoutDuration: 3000, cacheMaxAge: 300000 })
  const verify = tokenVerifier(config.auth, jwks)
  // Avoid inheriting arbitrary PGRST_* overrides from application secrets.
  const childEnv = Object.fromEntries(Object.entries(env).filter(([k]) => !k.startsWith('PGRST_')))
  const child = spawn(env.DATA_API_POSTGREST_BIN || '/usr/local/bin/postgrest', [], { env: { ...childEnv, ...config.postgrestEnv }, stdio: ['ignore', 'ignore', 'ignore'] })
  const server = createServer(config, verify)
  let stopping = false
  server.requestTimeout = limits.queryMs + 5000
  server.headersTimeout = 10000
  child.on('error', () => { console.error('PostgREST could not start'); process.exitCode = 1; server.close() })
  child.on('exit', code => { process.exitCode = stopping ? 0 : (code || 1); server.close() })
  for (const signal of ['SIGTERM', 'SIGINT']) process.once(signal, () => { stopping = true; server.close(); child.kill(signal) })
  process.on('SIGUSR1', () => child.kill('SIGUSR1'))
  server.listen(Number(env.PORT || 8080), '0.0.0.0')
}

export function tokenVerifier(auth, keys) {
  return async token => (await jwtVerify(token, keys, { issuer: auth.issuer, audience: auth.audience, algorithms: ['RS256', 'ES256'], requiredClaims: ['sub', 'exp'] })).payload
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) main().catch(() => { console.error('Data API configuration is invalid'); process.exitCode = 1 })
