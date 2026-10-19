import http from 'node:http'
import { readFileSync } from 'node:fs'
import { spawn } from 'node:child_process'
import { pathToFileURL } from 'node:url'
import pg from 'pg'
import { adminKeyHashes, createAdminHandler, deleteOwnAccount, goTrueAdmin, goTrueError, signingKey } from './admin.mjs'
import { createAuthzHandler, goTrueAuthenticator } from './authz.mjs'
import { createHookServer, createMailer, hookAddress, hookEnv, loadEmailTemplates, newHookSecret } from './email.mjs'
import { filterLogs } from './logs.mjs'
import { callbackTokens, createRealtimeAuthorizer } from './realtime.mjs'
import { storageBindings } from './storage.mjs'
import { createWorkflowStarter, workflowsConfig } from './workflows.mjs'
import { createEventPublisher, lifecycleConfig, publishesAfter, setLifecycleEvents } from './events.mjs'
import { createAnonymousPruner, createAuditPruner, createOrgAuditPruner } from './retention.mjs'
import { bodyAllowed, smsSendPath } from './sms.mjs'
import { createScimHandler } from './scim.mjs'
import { goMemoryLimit, gotrue, limits, pagePolicy, runtimeConfig } from './config.mjs'
import { accessTokenClaims, authorizationDetails, authorizationResource, bearerClaims, clientTokenAllowed, consentLocation, isClientToken, registrationOpen, wellKnownPath } from './oauth.mjs'

const prefix = '/auth/v1'
const authzPrefix = '/authz/v1/'
const pages = new Set(['/ui', '/ui/', '/ui/sign-in', '/ui/sign-up', '/ui/forgot-password', '/ui/reset-password', '/ui/invitation', '/ui/phone', '/ui/sso', '/ui/consent', '/ui/account'])
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

export function createServer({ upstreamPort = gotrue.port, bodyBytes = limits.bodyBytes, authz, ui, realtime, admin, settings, smsCountries = null, scim, oauth = null } = {}) {
  return http.createServer((req, res) => {
    // A third-party OAuth client's token is good here only for userinfo.
    if (isClientToken(bearerClaims(req.headers.authorization)) && !clientTokenAllowed(req.method, req.url)) return problem(res, 403, 'oauth_client_token')
    if (settings && req.url.split('?')[0] === '/settings/v1') return serveSettings(req, res, settings)
    if (realtime && req.url === '/realtime/authorize-channel') return realtime(req, res)
    if (admin && /^\/admin\/v1([/?]|$)/.test(req.url)) return admin(req, res)
    if (scim && /^\/scim\/v2([/?]|$)/.test(req.url)) return scim(req, res)
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
    const path = upstreamPath(req.url) ?? wellKnownPath(req.url)
    if (path === null || goTrueAdminPath(path)) return problem(res, 404, 'not_found')
    const declared = Number(req.headers['content-length'] ?? 0)
    if (!Number.isSafeInteger(declared) || declared > bodyBytes) return problem(res, 413, 'request_too_large')
    if (oauth && req.method === 'POST' && path.split('?')[0] === '/oauth/clients/register') {
      return oauth.registrationOpen().then(open => open ? proxy(req, res, path, upstreamPort, bodyBytes, oauth) : problem(res, 403, 'oauth_client_limit'), () => problem(res, 503, 'auth_unavailable'))
    }
    // A request that may text a number is read whole, checked against the
    // country allowlist and forwarded as read.
    if (smsCountries && smsSendPath(req.method, req.url)) {
      return readBody(req, res, bodyBytes, body => {
        if (!bodyAllowed(body, smsCountries)) return problem(res, 422, 'sms_country_not_allowed')
        const headers = forwardHeaders(req.headers)
        delete headers['content-length']
        upstreamRequest(req, res, path, upstreamPort, { ...headers, 'content-length': String(body.length) }, oauth).end(body)
      })
    }
    proxy(req, res, path, upstreamPort, bodyBytes, oauth)
  })
}

// upstreamRequest opens the request to GoTrue and relays its answer, with
// the OAuth server's consent redirect and an authorization's resource.
function upstreamRequest(req, res, path, upstreamPort, headers, oauth) {
  const upstream = http.request({ host: gotrue.host, port: upstreamPort, method: req.method, path, headers }, response => {
    const responseHeaders = forwardHeaders(response.headers)
    if (oauth && responseHeaders.location) responseHeaders.location = oauth.location(responseHeaders.location)
    const details = oauth && req.method === 'GET' && response.statusCode === 200 ? authorizationDetails.exec(path.split('?')[0]) : null
    if (details) return withResource(response, res, responseHeaders, oauth, details[1])
    res.writeHead(response.statusCode, responseHeaders)
    response.pipe(res)
  })
  upstream.on('error', () => problem(res, 502, 'auth_unavailable'))
  return upstream
}

function proxy(req, res, path, upstreamPort, bodyBytes, oauth) {
  const upstream = upstreamRequest(req, res, path, upstreamPort, forwardHeaders(req.headers), oauth)
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
}

// withResource adds the resource a client asked for to GoTrue's small view
// of an authorization, which the consent page shows.
function withResource(response, res, headers, oauth, id) {
  const chunks = []
  let size = 0
  response.on('data', chunk => { size += chunk.length; if (size <= limits.authzBodyBytes) chunks.push(chunk) })
  response.on('end', async () => {
    try {
      if (size > limits.authzBodyBytes) throw new Error('too large')
      const view = JSON.parse(Buffer.concat(chunks).toString('utf8'))
      const payload = JSON.stringify(view.redirect_url ? view : { ...view, resource: await oauth.resource(id) })
      delete headers['content-length']
      res.writeHead(200, { ...headers, 'content-length': Buffer.byteLength(payload) })
      res.end(payload)
    } catch {
      problem(res, 502, 'auth_unavailable')
    }
  })
}

// GoTrue runs as a supervised child on loopback. Either process exiting ends
// the instance so the platform restarts both together.
export function start(env = process.env) {
  const config = runtimeConfig(env)
  const secret = newHookSecret()
  const mailer = createMailer(config.email)
  // Short idle lifetime: a parked or restored instance must not reuse a
  // connection that the database closed while it slept.
  const pool = new pg.Pool({ connectionString: config.gotrueEnv.DATABASE_URL, max: limits.frontPool, idleTimeoutMillis: 5000 })
  pool.on('error', () => {})
  const tokenHook = config.oauth || config.orgClaims
  const accessToken = tokenHook ? payload => accessTokenClaims(pool, payload, { orgClaims: config.orgClaims }) : null
  const hooks = createHookServer({ secret, mailer, api: config.api, app: config.app, accessToken, templates: () => loadEmailTemplates(pool) })
  hooks.listen(hookAddress.port, hookAddress.host)
  const child = spawn(gotrue.binary, ['serve'], { env: { ...config.gotrueEnv, ...hookEnv(secret, { accessToken: tokenHook }), GOMEMLIMIT: goMemoryLimit() }, stdio: ['ignore', 'pipe', 'pipe'] })
  filterLogs(child.stdout, process.stdout)
  filterLogs(child.stderr, process.stderr)
  const invitations = { mailer, external: config.external, app: config.app }
  const ui = { siteURL: config.gotrueEnv.GOTRUE_SITE_URL, redirectURLs: config.redirects, app: config.app, branding: config.branding, captcha: config.captcha }
  const workflows = workflowsConfig(env)
  const goTrue = goTrueAdmin(signingKey(config.gotrueEnv.GOTRUE_JWT_KEYS))
  const orgSSO = config.orgSSO ? { admin: goTrue, goTrueError, external: config.external } : null
  const deleteAccount = config.selfDeletion ? claims => deleteOwnAccount({ pool, admin: goTrue }, claims) : null
  const authz = createAuthzHandler({ pool, authenticate: goTrueAuthenticator(), origins: config.origins, invitations, storage: storageBindings(env), workflows: workflows && createWorkflowStarter(workflows), orgSSO, teamEnabled: config.productControlTeamEnabled, emailEnabled: config.productControlEmailEnabled, deleteAccount, orgClaims: config.orgClaims })
  const realtime = createRealtimeAuthorizer({ pool, tokens: callbackTokens(env) })
  // The dashboard is served from the Gregale API origin.
  let dashboard = []
  try { dashboard = [new URL(env.AUTH_GREGALE_API_URL).origin] } catch { /* no dashboard origin sealed */ }
  const admin = createAdminHandler({ pool, admin: goTrue, keys: adminKeyHashes(env), origins: dashboard, external: config.external, emails: { mailer, api: config.api, app: config.app } })
  const scim = createScimHandler({ pool, admin: goTrue, base: config.external })
  const oauth = config.oauth ? {
    location: location => consentLocation(location, config.gotrueEnv.GOTRUE_SITE_URL, config.external),
    registrationOpen: () => registrationOpen(pool),
    resource: id => authorizationResource(pool, id),
  } : null
  const server = createServer({ authz, ui, realtime, admin, settings: config.settings, smsCountries: config.smsCountries, scim, oauth })
  // Lifecycle events publish after requests that may change users, and
  // once at startup for anything recorded while the app was parked.
  const lifecycle = lifecycleConfig(env)
  const events = lifecycle && createEventPublisher({ pool, config: lifecycle })
  setLifecycleEvents(pool, Boolean(lifecycle)).then(() => events?.poke(), err => {
    console.error(JSON.stringify({ level: 'error', event: 'lifecycle_events_setup_failed', sqlstate: err?.code ?? null }))
  })
  // Activity retention prunes on the same request-driven schedule.
  const pruners = [createAuditPruner({ pool, days: config.auditRetentionDays }), createOrgAuditPruner({ pool, days: config.auditRetentionDays }), createAnonymousPruner({ pool, days: config.anonymousRetentionDays })]
  for (const pruner of pruners) pruner.poke()
  server.on('request', (req, res) => {
    if (publishesAfter(req)) res.on('finish', () => { events?.poke(); for (const pruner of pruners) pruner.poke() })
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
