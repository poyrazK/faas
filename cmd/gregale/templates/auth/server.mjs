import http from 'node:http'
import { readFileSync } from 'node:fs'
import { spawn } from 'node:child_process'
import { pathToFileURL } from 'node:url'
import pg from 'pg'
import { adminKeyHashes, createAdminHandler, goTrueAdmin, signingKey } from './admin.mjs'
import { createAuthzHandler, goTrueAuthenticator } from './authz.mjs'
import { createHookServer, createMailer, hookAddress, hookEnv, newHookSecret } from './email.mjs'
import { filterLogs } from './logs.mjs'
import { callbackTokens, createRealtimeAuthorizer } from './realtime.mjs'
import { storageBindings } from './storage.mjs'
import { createWorkflowStarter, workflowsConfig } from './workflows.mjs'
import { createEventPublisher, lifecycleConfig, publishesAfter, setLifecycleEvents } from './events.mjs'
import { createAuditPruner } from './retention.mjs'
import { bodyAllowed, smsSendPath } from './sms.mjs'
import { gotrue, limits, pagePolicy, runtimeConfig } from './config.mjs'

const prefix = '/auth/v1'
const authzPrefix = '/authz/v1/'
const pages = new Set(['/ui', '/ui/', '/ui/sign-in', '/ui/sign-up', '/ui/forgot-password', '/ui/reset-password', '/ui/invitation', '/ui/phone', '/ui/sso'])
const assets = { '/ui/app.js': 'text/javascript; charset=utf-8', '/ui/style.css': 'text/css; charset=utf-8' }
// The hosted pages load only their own script and style and call only this
// origin, plus a configured logo's origin and CAPTCHA provider; tokens
// arrive and leave in URL fragments, never in Referer.
const pageHeaders = policy => ({
  'Content-Security-Policy': policy,
  'X-Content-Type-Options': 'nosniff', 'X-Frame-Options': 'DENY', 'Referrer-Policy': 'no-referrer', 'Cache-Control': 'no-cache',
})

function readUI(name) {
  return readFileSync(new URL(`./ui/${name}`, import.meta.url))
}

// serveUI answers /ui requests; it returns false for anything else.
function serveUI(req, res, ui) {
  const path = req.url.split('?')[0]
  if (path !== '/ui' && !path.startsWith('/ui/')) return false
  if (req.method !== 'GET' && req.method !== 'HEAD') { problem(res, 405, 'method_not_allowed'); return true }
  const branding = ui.branding ?? { logoURL: null, color: null }
  const headers = pageHeaders(pagePolicy(branding, ui.captcha))
  if (path === '/ui/config.json') {
    res.writeHead(200, { ...headers, 'Content-Type': 'application/json', 'Cache-Control': 'no-store' })
    res.end(JSON.stringify({
      site_url: ui.siteURL, redirect_urls: ui.redirectURLs, app: ui.app, logo_url: branding.logoURL,
      captcha: ui.captcha ? { provider: ui.captcha.provider, site_key: ui.captcha.siteKey, script: ui.captcha.script } : null,
    }))
  } else if (path === '/ui/brand.css') {
    // The accent colour is validated as #rrggbb, so it cannot break out.
    res.writeHead(200, { ...headers, 'Content-Type': 'text/css; charset=utf-8' })
    res.end(branding.color ? `:root { --accent: ${branding.color}; }\n` : '')
  } else if (assets[path]) {
    res.writeHead(200, { ...headers, 'Content-Type': assets[path] })
    res.end(readUI(path.slice('/ui/'.length)))
  } else if (pages.has(path)) {
    res.writeHead(200, { ...headers, 'Content-Type': 'text/html; charset=utf-8' })
    res.end(readUI('index.html'))
  } else {
    problem(res, 404, 'not_found')
  }
  return true
}
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

// GoTrue's admin endpoints need a service_role token that only this front
// mints, so the public path never needs them; owners use /admin/v1.
export function goTrueAdminPath(path) {
  let decoded
  try { decoded = decodeURIComponent(path.split('?')[0]) } catch { return true }
  return /^\/(admin|invite)(\/|$)/.test(decoded.replace(/\/{2,}/g, '/').toLowerCase())
}

// upstreamPath maps /auth/v1/<path> to GoTrue's root without decoding, so a
// path GoTrue would reject is never rewritten into one it accepts.
export function upstreamPath(url) {
  if (url === prefix) return '/'
  if (url.startsWith(prefix + '/') || url.startsWith(prefix + '?')) return url.slice(prefix.length).replace(/^\?/, '/?')
  return null
}

// /settings/v1 is public: the CLI and the dashboard read the running app's
// configuration here because app secrets are write-only.
function serveSettings(req, res, settings) {
  if (req.method !== 'GET') return problem(res, 405, 'method_not_allowed')
  res.writeHead(200, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store', 'Access-Control-Allow-Origin': '*', 'X-Content-Type-Options': 'nosniff' })
  res.end(JSON.stringify(settings))
}

// readBody buffers a request body up to bodyBytes for an SMS country check.
function readBody(req, res, bodyBytes, done) {
  const chunks = []
  let received = 0
  req.on('data', chunk => {
    received += chunk.length
    if (received > bodyBytes) { problem(res, 413, 'request_too_large'); req.destroy() } else chunks.push(chunk)
  })
  req.on('end', () => { if (received <= bodyBytes) done(Buffer.concat(chunks)) })
}

export function createServer({ upstreamPort = gotrue.port, bodyBytes = limits.bodyBytes, authz, ui, realtime, admin, settings, smsCountries = null } = {}) {
  return http.createServer((req, res) => {
    if (settings && req.url.split('?')[0] === '/settings/v1') return serveSettings(req, res, settings)
    if (realtime && req.url === '/realtime/authorize-channel') return realtime(req, res)
    if (admin && /^\/admin\/v1([/?]|$)/.test(req.url)) return admin(req, res)
    if (authz && req.url.startsWith(authzPrefix)) return authz(req, res, '/' + req.url.slice(authzPrefix.length).split('?')[0])
    if (ui && serveUI(req, res, ui)) return
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
    if (path === null || goTrueAdminPath(path)) return problem(res, 404, 'not_found')
    const declared = Number(req.headers['content-length'] ?? 0)
    if (!Number.isSafeInteger(declared) || declared > bodyBytes) return problem(res, 413, 'request_too_large')
    const open = headers => {
      const upstream = http.request({ host: gotrue.host, port: upstreamPort, method: req.method, path, headers }, response => {
        res.writeHead(response.statusCode, forwardHeaders(response.headers))
        response.pipe(res)
      })
      upstream.on('error', () => problem(res, 502, 'auth_unavailable'))
      return upstream
    }
    // A request that may text a number is read whole, checked against the
    // country allowlist and forwarded as read.
    if (smsCountries && smsSendPath(req.method, req.url)) {
      return readBody(req, res, bodyBytes, body => {
        if (!bodyAllowed(body, smsCountries)) return problem(res, 422, 'sms_country_not_allowed')
        const headers = forwardHeaders(req.headers)
        delete headers['content-length']
        open({ ...headers, 'content-length': String(body.length) }).end(body)
      })
    }
    const upstream = open(forwardHeaders(req.headers))
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
  const child = spawn(gotrue.binary, ['serve'], { env: { ...config.gotrueEnv, ...hookEnv(secret) }, stdio: ['ignore', 'pipe', 'pipe'] })
  filterLogs(child.stdout, process.stdout)
  filterLogs(child.stderr, process.stderr)
  // Short idle lifetime: a parked or restored instance must not reuse a
  // connection that the database closed while it slept.
  const pool = new pg.Pool({ connectionString: config.gotrueEnv.DATABASE_URL, max: limits.frontPool, idleTimeoutMillis: 5000 })
  pool.on('error', () => {})
  const invitations = { mailer, external: config.external, app: config.app }
  const ui = { siteURL: config.gotrueEnv.GOTRUE_SITE_URL, redirectURLs: config.redirects, app: config.app, branding: config.branding, captcha: config.captcha }
  const workflows = workflowsConfig(env)
  const authz = createAuthzHandler({ pool, authenticate: goTrueAuthenticator(), origins: config.origins, invitations, storage: storageBindings(env), workflows: workflows && createWorkflowStarter(workflows) })
  const realtime = createRealtimeAuthorizer({ pool, tokens: callbackTokens(env) })
  // The dashboard is served from the Gregale API origin.
  let dashboard = []
  try { dashboard = [new URL(env.AUTH_GREGALE_API_URL).origin] } catch { /* no dashboard origin sealed */ }
  const admin = createAdminHandler({ pool, admin: goTrueAdmin(signingKey(config.gotrueEnv.GOTRUE_JWT_KEYS)), keys: adminKeyHashes(env), origins: dashboard })
  const server = createServer({ authz, ui, realtime, admin, settings: config.settings, smsCountries: config.smsCountries })
  // Lifecycle events publish after requests that may change users, and
  // once at startup for anything recorded while the app was parked.
  const lifecycle = lifecycleConfig(env)
  const events = lifecycle && createEventPublisher({ pool, config: lifecycle })
  setLifecycleEvents(pool, Boolean(lifecycle)).then(() => events?.poke(), err => {
    console.error(JSON.stringify({ level: 'error', event: 'lifecycle_events_setup_failed', sqlstate: err?.code ?? null }))
  })
  // Activity retention prunes on the same request-driven schedule.
  const pruner = createAuditPruner({ pool, days: config.auditRetentionDays })
  pruner.poke()
  server.on('request', (req, res) => {
    if (publishesAfter(req)) res.on('finish', () => { events?.poke(); pruner.poke() })
  })
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
