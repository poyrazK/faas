import http from 'node:http'
import { spawn } from 'node:child_process'
import { pathToFileURL } from 'node:url'
import pg from 'pg'
import { createAuthzHandler, goTrueAuthenticator } from './authz.mjs'
import { createHookServer, createMailer, hookAddress, hookEnv, newHookSecret } from './email.mjs'
import { gotrue, limits, runtimeConfig } from './config.mjs'

const prefix = '/auth/v1'
const authzPrefix = '/authz/v1/'
const hopByHop = new Set(['connection', 'keep-alive', 'proxy-connection', 'transfer-encoding', 'upgrade', 'te', 'trailer'])

function problem(res, status, code) {
  if (res.headersSent) return res.destroy()
  res.writeHead(status, { 'Content-Type': 'application/problem+json', 'Cache-Control': 'no-store' })
  res.end(JSON.stringify({ status, code }))
}

function forwardHeaders(headers) {
  const out = {}
  for (const [name, value] of Object.entries(headers)) {
    if (!hopByHop.has(name)) out[name] = value
  }
  return out
}

// upstreamPath maps /auth/v1/<path> to GoTrue's root without decoding, so a
// path GoTrue would reject is never rewritten into one it accepts.
export function upstreamPath(url) {
  if (url === prefix) return '/'
  if (url.startsWith(prefix + '/') || url.startsWith(prefix + '?')) return url.slice(prefix.length).replace(/^\?/, '/?')
  return null
}

export function createServer({ upstreamPort = gotrue.port, bodyBytes = limits.bodyBytes, authz } = {}) {
  return http.createServer((req, res) => {
    if (authz && req.url.startsWith(authzPrefix)) return authz(req, res, '/' + req.url.slice(authzPrefix.length).split('?')[0])
    if (req.url === '/healthz') {
      if (req.method !== 'GET') return problem(res, 405, 'method_not_allowed')
      const probe = http.get({ host: gotrue.host, port: upstreamPort, path: '/health', timeout: 2000 }, upstream => {
        upstream.resume()
        if (upstream.statusCode !== 200) return problem(res, 503, 'auth_unavailable')
        res.writeHead(200, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' })
        res.end('{"ready":true}')
      })
      probe.on('timeout', () => probe.destroy())
      probe.on('error', () => problem(res, 503, 'auth_unavailable'))
      return
    }
    const path = upstreamPath(req.url)
    if (path === null) return problem(res, 404, 'not_found')
    const declared = Number(req.headers['content-length'] ?? 0)
    if (!Number.isSafeInteger(declared) || declared > bodyBytes) return problem(res, 413, 'request_too_large')
    const upstream = http.request({ host: gotrue.host, port: upstreamPort, method: req.method, path, headers: forwardHeaders(req.headers) }, response => {
      res.writeHead(response.statusCode, forwardHeaders(response.headers))
      response.pipe(res)
    })
    upstream.on('error', () => problem(res, 502, 'auth_unavailable'))
    let received = 0
    req.on('data', chunk => {
      received += chunk.length
      // Chunked bodies have no declared length; stop them at the same bound.
      if (received > bodyBytes) {
        upstream.destroy()
        problem(res, 413, 'request_too_large')
        req.destroy()
      }
    })
    req.pipe(upstream)
  })
}

// GoTrue runs as a supervised child on loopback. Either process exiting ends
// the instance so the platform restarts both together.
export function start(env = process.env) {
  const config = runtimeConfig(env)
  const secret = newHookSecret()
  const mailer = createMailer(config.email)
  const hooks = createHookServer({ secret, mailer, api: config.api, app: config.app })
  hooks.listen(hookAddress.port, hookAddress.host)
  const child = spawn(gotrue.binary, ['serve'], { env: { ...config.gotrueEnv, ...hookEnv(secret) }, stdio: ['ignore', 'inherit', 'inherit'] })
  // Short idle lifetime: a parked or restored instance must not reuse a
  // connection that the database closed while it slept.
  const pool = new pg.Pool({ connectionString: config.gotrueEnv.DATABASE_URL, max: limits.frontPool, idleTimeoutMillis: 5000 })
  pool.on('error', () => {})
  const invitations = { mailer, external: config.external, app: config.app }
  const server = createServer({ authz: createAuthzHandler({ pool, authenticate: goTrueAuthenticator(), origins: config.origins, invitations }) })
  child.on('exit', code => {
    console.error(JSON.stringify({ level: 'error', event: 'gotrue_exit', code }))
    server.close()
    process.exit(code === 0 ? 1 : code ?? 1)
  })
  for (const signal of ['SIGTERM', 'SIGINT']) {
    process.on(signal, () => {
      child.kill(signal)
      server.close(() => process.exit(0))
    })
  }
  server.listen(Number(env.PORT ?? 8080))
  return { server, child }
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) start()
