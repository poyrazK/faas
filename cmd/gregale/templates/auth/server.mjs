import http from 'node:http'
import { readFileSync } from 'node:fs'
import { spawn } from 'node:child_process'
import { pathToFileURL } from 'node:url'
import pg from 'pg'
import { adminKeyHashes, adminKeySummary, createAdminHandler, deleteOwnAccount, goTrueAdmin, goTrueError, signingKey } from './admin.mjs'
import { createAuthzHandler, goTrueAuthenticator } from './authz.mjs'
import { createHookServer, createMailer, hookAddress, hookEnv, loadEmailTemplates, newHookSecret } from './email.mjs'
import { filterLogs } from './logs.mjs'
import { callbackTokens, createRealtimeAuthorizer } from './realtime.mjs'
import { storageBindings } from './storage.mjs'
import { createWorkflowStarter, workflowsConfig } from './workflows.mjs'
import { createEventPublisher, lifecycleConfig, publishesAfter, setLifecycleEvents } from './events.mjs'
import { createAnonymousPruner, createAttemptPruner, createAuditPruner, createOrgAuditPruner, createSecurityEventPruner, createSignInSeenPruner, createUnusedClientPruner } from './retention.mjs'
import { addressBucket, budgetKey, budgets, createAttempts, goQueryValue, guardedPath, outcome, passwordEntries, secondStepEntries, signInSubject } from './attempts.mjs'
import { createSignInSecurity, deviceLabel, serverAgent, sessionFromAnswer, sessionFromLocation } from './security.mjs'
import { createSends, messageBudgets, sendRoute } from './sends.mjs'
import { createSecurityLog, networkLabel } from './securitylog.mjs'
import { bodyAllowed, smsSendPath } from './sms.mjs'
import { createScimHandler } from './scim.mjs'
import { signupRefusal } from './signups.mjs'
import { impersonated, impersonationBlocked } from './impersonation.mjs'
import { setDomainJoin } from './orgsso.mjs'
import { goMemoryLimit, gotrue, limits, pagePolicy, runtimeConfig } from './config.mjs'
import { accessTokenClaims, authorizationDetails, authorizationResource, bearerClaims, clientTokenAllowed, consentLocation, isClientToken, registeredClients, wellKnownPath } from './oauth.mjs'
import { createAuthenticator, createRateLimiter, issueToken, isServiceAccountToken, OAuthError, serviceAccountAllowed, tokenRequest, verificationKeys, verifyToken } from './serviceaccounts.mjs'

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
      web3: ui.web3 ?? { ethereum: false, solana: false },
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
const limitsDocs = 'https://gregale.dev/docs/application-auth#current-limits'
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

// serveAdminKey answers /settings/v1/admin-keys/<ID> with one admin key's
// scopes and expiry, for `gregale auth admin-key list`. A caller must
// already hold a random ID, which only the owner's secret list shows, and
// each address has budgets.adminKeyLookup lookups an hour, so the IDs cannot
// be enumerated. No browser origin may read it. A path that is not an ID is
// refused before the budget, so scanners never reach the database.
async function serveAdminKey(req, res, adminKeys, attempts) {
  if (req.method !== 'GET') return problem(res, 405, 'method_not_allowed')
  const id = /^\/settings\/v1\/admin-keys\/([0-9A-F]{8})$/.exec(req.url.split('?')[0])?.[1]
  if (!id) return problem(res, 404, 'not_found')
  try {
    if (attempts) {
      const { count, wait } = await attempts.take({ key: budgetKey('admin-key-lookup', addressBucket(clientAddress(req))), budget: budgets.adminKeyLookup })
      if (wait > 0) return limited(res, 'too_many_lookups', budgets.adminKeyLookup, count, wait)
    }
  } catch (err) {
    console.error(JSON.stringify({ level: 'error', event: 'admin_key_lookup_failed', sqlstate: err?.code ?? null }))
    return problem(res, 503, 'auth_unavailable')
  }
  const key = adminKeys.find(candidate => candidate.id === id)
  if (!key) return problem(res, 404, 'not_found')
  res.writeHead(200, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store', 'X-Content-Type-Options': 'nosniff' })
  res.end(JSON.stringify(key))
}

// limited answers 429 for a spent budget, with the limit, the count so far,
// the window and where the limits are documented.
function limited(res, code, budget, observed, wait, extra = {}) {
  if (res.headersSent) return res.destroy()
  res.writeHead(429, { 'Content-Type': 'application/problem+json', 'Cache-Control': 'no-store', 'Retry-After': String(wait) })
  res.end(JSON.stringify({ status: 429, code, ...extra, limit: budget.limit, observed, window_seconds: budget.seconds, retry_after: wait, docs_url: limitsDocs }))
}

// budgetName names an attempt budget in security events.
function budgetName(budget) {
  return budget === budgets.passwordAddress ? 'password_address' : budget === budgets.passwordAccount ? 'password_account' : 'second_step'
}

// tokenRoute names a request whose answer may hand a client a session: a
// token grant (by its grant type), a verified one-time code, or a sign-up
// that signs the person in at once.
function tokenRoute(method, path) {
  if (method !== 'POST') return null
  const [raw, query = ''] = path.split('?')
  const route = raw.replace(/\/+$/, '')
  if (route === '/token') return goQueryValue(query, 'grant_type') ?? ''
  return route === '/verify' || route === '/signup' ? route.slice(1) : null
}

function logFailure(event) {
  return err => console.error(JSON.stringify({ level: 'error', event, sqlstate: err?.code ?? null }))
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

// tokenReply answers the token endpoint as RFC 6749 section 5 says.
function tokenReply(res, status, payload, headers = {}) {
  if (res.headersSent) return res.destroy()
  res.writeHead(status, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store', Pragma: 'no-cache', ...headers })
  res.end(JSON.stringify(payload))
}

export function createServer({ upstreamPort = gotrue.port, bodyBytes = limits.bodyBytes, authz, ui, realtime, admin, settings, smsCountries = null, scim, oauth = null, serviceAccounts = null, attempts = null, adminKeys = [], signIns = null, sends = null, captcha = false, securityLog = null } = {}) {
  // signedIn records a session GoTrue handed this client (security.mjs). A
  // session only a redirect showed (an email link scanner may follow it)
  // neither alerts nor trusts its network, and a refreshed one trusts only
  // a network its account was already seen on (record's trust).
  const signedIn = (req, session, { alert = true, trust = true } = {}) => {
    if (!signIns || !session || serverAgent(req.headers['user-agent'])) return
    const address = clientAddress(req), userAgent = req.headers['user-agent']
    signIns.record({ ...session, address, userAgent, alert, trust })
      .then(alerted => alerted && securityLog?.record('new_device_alert', { userId: session.userId, address, detail: { device: deviceLabel(userAgent) } }))
      .catch(logFailure('sign_in_record_failed'))
  }
  return http.createServer((req, res) => {
    // A third-party OAuth client's token is good here only for userinfo.
    const bearer = bearerClaims(req.headers.authorization)
    if (isClientToken(bearer) && !clientTokenAllowed(req.method, req.url)) return problem(res, 403, 'oauth_client_token')
    // A session support staff opened as a user cannot change the account.
    if (impersonated(bearer) && impersonationBlocked(req.method, req.url)) return problem(res, 403, 'impersonation_restricted')
    // A service account's token is for the application's API, not this host.
    if (isServiceAccountToken(bearer) && !serviceAccountAllowed(req.method, req.url)) return problem(res, 403, 'service_account_token')
    if (settings && req.url.split('?')[0] === '/settings/v1') return serveSettings(req, res, settings)
    if (settings && req.url.split('?')[0].startsWith('/settings/v1/admin-keys/')) return serveAdminKey(req, res, adminKeys, attempts)
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
    // GoTrue gets no bearer token whose claims the checks above could not read.
    if (bearer === false) return problem(res, 401, 'invalid_token')
    const declared = Number(req.headers['content-length'] ?? 0)
    if (!Number.isSafeInteger(declared) || declared > bodyBytes) return problem(res, 413, 'request_too_large')
    if (oauth && req.method === 'POST' && path.split('?')[0].replace(/\/+$/, '') === '/oauth/clients/register') {
      return registerClient(req, res, path, upstreamPort, bodyBytes, oauth, attempts)
    }
    // The front answers the client-credentials grant for service accounts
    // and passes every other token request to GoTrue as read.
    if (serviceAccounts && req.method === 'POST' && path.split('?')[0] === '/oauth/token') {
      return readBody(req, res, bodyBytes, body => {
        let request
        try { request = tokenRequest(req.headers['content-type'], body, req.headers.authorization) } catch (err) { return tokenReply(res, err.status, { error: err.error }, err.headers) }
        if (!request) {
          const headers = forwardHeaders(req.headers)
          delete headers['content-length']
          return upstreamRequest(req, res, path, upstreamPort, { ...headers, 'content-length': String(body.length) }, { oauth, signedIn }).end(body)
        }
        // The public gateway replaces X-Real-IP with the verified client address.
        serviceAccounts.issue({ ...request, address: String(req.headers['x-real-ip'] ?? req.socket.remoteAddress) }).then(payload => tokenReply(res, 200, payload), err => {
          if (err instanceof OAuthError) return tokenReply(res, err.status, { error: err.error }, err.headers)
          console.error(JSON.stringify({ level: 'error', event: 'service_account_token_failed', sqlstate: err?.code ?? null }))
          tokenReply(res, 503, { error: 'temporarily_unavailable' })
        })
      })
    }
    // Password sign-ins and second-step codes spend failed-attempt budgets
    // the instances share (attempts.mjs).
    const guarded = attempts ? guardedPath(req.method, path) : null
    if (guarded) return readBody(req, res, bodyBytes, body => guardAttempt({ req, res, path, upstreamPort, body, oauth, attempts, kind: guarded, bearer, signIns, signedIn, captcha, securityLog }))
    // A request that may send a message is read whole, checked against the
    // SMS country allowlist and the message budgets (sends.mjs), and
    // forwarded as read.
    const send = sends ? sendRoute(req.method, path) : null
    const sms = smsCountries !== null && smsSendPath(req.method, req.url)
    if (send || sms) return readBody(req, res, bodyBytes, body => forwardSend({ req, res, path, upstreamPort, body, oauth, signedIn, send, sends, smsCountries: sms ? smsCountries : null, bearer, securityLog }))
    // Other answers that hand out a session are read to record it.
    const issuing = signIns ? tokenRoute(req.method, path) : null
    const observe = issuing === null ? null : async (status, answer) => { if (status === 200) signedIn(req, sessionFromAnswer(answer), { trust: issuing === 'refresh_token' ? 'known' : true }) }
    proxy(req, res, path, upstreamPort, bodyBytes, { oauth, observe, signedIn })
  })
}

// clientAddress is the verified client address the public gateway puts in
// X-Real-IP.
function clientAddress(req) {
  return String(req.headers['x-real-ip'] ?? req.socket.remoteAddress)
}

// registerClient passes a dynamic OAuth client registration to GoTrue while
// the caller's address (an IPv4 address or IPv6 /64) has registrations left
// in budgets.oauthRegistration, the app holds fewer than limits.oauthClients
// clients, and fewer than limits.oauthUnusedClients of them are
// self-registered clients nobody has used. Clients people use and those the
// owner made therefore always keep limits.oauthClients minus
// limits.oauthUnusedClients places, whoever floods registration; unused
// clients are pruned limits.oauthUnusedClientHours after they register
// (retention.mjs), and owners can still register clients with an admin key.
async function registerClient(req, res, path, upstreamPort, bodyBytes, oauth, attempts) {
  try {
    if (attempts) {
      const budget = budgets.oauthRegistration
      const { count, wait } = await attempts.take({ key: budgetKey('oauth-registration', addressBucket(clientAddress(req))), budget })
      if (wait > 0) { req.resume(); return limited(res, 'too_many_registrations', budget, count, wait) }
    }
    const { clients, unused } = await oauth.registeredClients()
    const refusal = clients >= limits.oauthClients ? [403, 'oauth_client_limit', limits.oauthClients, clients]
      : unused >= limits.oauthUnusedClients ? [429, 'too_many_unused_clients', limits.oauthUnusedClients, unused] : null
    if (refusal) {
      const [status, code, limit, observed] = refusal
      req.resume()
      if (res.headersSent) return res.destroy()
      res.writeHead(status, { 'Content-Type': 'application/problem+json', 'Cache-Control': 'no-store', ...(status === 429 ? { 'Retry-After': '3600' } : {}) })
      return res.end(JSON.stringify({ status, code, limit, observed, docs_url: limitsDocs }))
    }
  } catch (err) {
    console.error(JSON.stringify({ level: 'error', event: 'oauth_registration_failed', sqlstate: err?.code ?? null }))
    req.resume()
    return problem(res, 503, 'auth_unavailable')
  }
  proxy(req, res, path, upstreamPort, bodyBytes, { oauth })
}

// guardAttempt counts a password sign-in or second-step code against its
// budgets, refuses it when that overspends one, and otherwise forwards it
// and settles the count from GoTrue's answer before relaying it. Past the
// account-wide password budget a sign-in still goes ahead from a network
// the account signed in from recently (security.mjs), or, up to
// limits.trustedBudgetMultiplier times the budget, when the app checks a
// CAPTCHA, which GoTrue then verifies on every password sign-in: guessing
// from many addresses can slow an account but not lock its owner out on
// their own network. A refused attempt, or one GoTrue never answered, gives
// back what it counted, and a success clears
// the address's budget but not the account's, so the owner signing in does
// not hand a guesser a fresh budget.
async function guardAttempt({ req, res, path, upstreamPort, body, oauth, attempts, kind, bearer, signIns, signedIn, captcha, securityLog }) {
  let entries, subject = null
  if (kind === 'password') {
    try { subject = signInSubject(body) } catch (err) { return problem(res, 400, err.message === 'ambiguous' ? 'ambiguous_sign_in' : 'bad_json') }
    entries = subject ? passwordEntries(subject, clientAddress(req)) : []
  } else {
    entries = secondStepEntries(bearer)
  }
  const counted = []
  const giveBack = () => counted.length ? attempts.release(counted).catch(logFailure('attempt_budget_failed')) : undefined
  // Security events name the account by its sign-in subject, or the user a
  // second step's token names (securitylog.mjs).
  const address = clientAddress(req), who = kind === 'password' ? { subject } : { userId: bearer?.sub }
  const alike = [subject ?? (typeof bearer?.sub === 'string' ? bearer.sub.toLowerCase() : null), networkLabel(address)]
  let captchaWaived = false
  try {
    for (const entry of entries) {
      const { count, wait } = await attempts.take(entry)
      counted.push(entry)
      if (wait === 0) continue
      if (entry.budget === budgets.passwordAccount) {
        if (captcha && count <= entry.budget.limit * limits.trustedBudgetMultiplier) {
          // Recorded once GoTrue, which checks the CAPTCHA, has answered.
          captchaWaived = true
          continue
        }
        if (await signIns?.trusted(subject, address)) {
          securityLog?.record('trusted_pass', { ...who, address, alike, detail: { budget: 'password_account' } })
          continue
        }
      }
      await giveBack()
      securityLog?.record('held', { ...who, address, hold: entry.key, detail: { budget: budgetName(entry.budget), limit: entry.budget.limit, until: new Date(Date.now() + wait * 1000).toISOString() } })
      return limited(res, 'too_many_attempts', entry.budget, count - 1, wait, { error_code: 'too_many_attempts' })
    }
  } catch (err) {
    logFailure('attempt_budget_failed')(err)
    await giveBack()
    return problem(res, 503, 'auth_unavailable')
  }
  const headers = forwardHeaders(req.headers)
  delete headers['content-length']
  const observe = async (status, answer) => {
    const result = entries.length ? outcome(kind, status, answer) : null
    try {
      // A success clears the address's budget and gives back what it
      // counted against the account's, whose failures stay.
      if (result === 'success') {
        await attempts.clear(entries.filter(entry => entry.budget !== budgets.passwordAccount))
        await attempts.release(entries.filter(entry => entry.budget === budgets.passwordAccount))
      }
      else if (result !== 'failure' && entries.length) await attempts.release(entries)
    } catch (err) {
      logFailure('attempt_budget_failed')(err)
    }
    // GoTrue judged the password (right or wrong) only after it accepted the
    // CAPTCHA; its rate-limit and validation refusals come before that.
    if (captchaWaived && (result === 'success' || result === 'failure')) securityLog?.record('captcha_pass', { ...who, address, alike, detail: { budget: 'password_account' } })
    if (result === 'failure') securityLog?.record(kind === 'password' ? 'password_failed' : 'code_failed', { ...who, address, alike })
    if (status === 200) signedIn(req, sessionFromAnswer(answer))
  }
  upstreamRequest(req, res, path, upstreamPort, { ...headers, 'content-length': String(body.length) }, { oauth, observe, signedIn, onError: giveBack }).end(body)
}

// forwardSend checks a request that may send a message: a texted number
// against the country allowlist, then the message budgets. A budget GoTrue's
// refusal shows was not spent is given back.
async function forwardSend({ req, res, path, upstreamPort, body, oauth, signedIn, send, sends, smsCountries, bearer, securityLog }) {
  if (smsCountries && !bodyAllowed(body, smsCountries)) return problem(res, 422, 'sms_country_not_allowed')
  let taken = [], passes = []
  if (send) {
    let recipients
    try {
      recipients = await sends.recipients(send, body, bearer)
    } catch (err) {
      if (err.message === 'ambiguous') return problem(res, 400, 'ambiguous_recipient')
      logFailure('message_budget_failed')(err)
      return problem(res, 503, 'auth_unavailable')
    }
    try {
      const result = await sends.take(recipients, clientAddress(req))
      if (result.refused) {
        const { name, key, recipient, budget, count, wait } = result.refused
        securityLog?.record('message_limited', { subject: recipient, address: clientAddress(req), hold: key, detail: { budget: `message_${name}`, limit: budget.limit, until: new Date(Date.now() + wait * 1000).toISOString() } })
        return limited(res, 'too_many_messages', budget, count, wait, { error_code: 'too_many_messages', scope: name })
      }
      taken = result.taken
      passes = result.passes ?? []
    } catch (err) {
      logFailure('message_budget_failed')(err)
      return problem(res, 503, 'auth_unavailable')
    }
  }
  const headers = forwardHeaders(req.headers)
  delete headers['content-length']
  const giveBack = () => taken.length ? sends.release(taken).catch(logFailure('message_budget_failed')) : undefined
  // A sign-up that signs the person in at once hands over a session.
  const observe = async (status, answer) => {
    // An email or phone change GoTrue applied without writing to anyone
    // (the same value, or no confirmation needed) sent nothing.
    if (status >= 400 || (send?.user && status === 200 && !answer?.new_email && !answer?.new_phone)) await giveBack()
    // A trusted network's pass of a spent recipient budget, once GoTrue took
    // the request.
    else for (const recipient of passes) securityLog?.record('trusted_pass', { subject: recipient, address: clientAddress(req), alike: [recipient, 'messages', networkLabel(clientAddress(req))], detail: { budget: 'message_recipient' } })
    if (status === 200) signedIn(req, sessionFromAnswer(answer))
  }
  upstreamRequest(req, res, path, upstreamPort, { ...headers, 'content-length': String(body.length) }, { oauth, observe, signedIn, onError: giveBack }).end(body)
}

// upstreamRequest opens the request to GoTrue and relays its answer, with
// the OAuth server's consent redirect and an authorization's resource. With
// observe, the answer is read whole and shown to it before it is relayed. A
// redirect that hands the browser a session in its fragment is recorded
// with signedIn.
function upstreamRequest(req, res, path, upstreamPort, headers, { oauth = null, observe = null, signedIn = null, onError = null } = {}) {
  let failed = false
  const dropped = () => {
    if (failed) return
    failed = true
    onError?.()
    problem(res, 502, 'auth_unavailable')
  }
  const upstream = http.request({ host: gotrue.host, port: upstreamPort, method: req.method, path, headers }, response => {
    const responseHeaders = forwardHeaders(response.headers)
    if (signedIn && typeof responseHeaders.location === 'string' && responseHeaders.location.includes('access_token=')) signedIn(req, sessionFromLocation(responseHeaders.location), { alert: false, trust: false })
    if (oauth && responseHeaders.location) responseHeaders.location = oauth.location(responseHeaders.location)
    // A dropped answer gives back what the request counted and answers 502.
    response.on('error', dropped)
    response.on('aborted', dropped)
    if (observe) return observed(response, res, responseHeaders, observe)
    const details = oauth && req.method === 'GET' && response.statusCode === 200 ? authorizationDetails.exec(path.split('?')[0]) : null
    if (details) return withResource(response, res, responseHeaders, oauth, details[1])
    if (oauth?.clientCredentials && req.method === 'GET' && response.statusCode === 200 && /^\/\.well-known\/(oauth-authorization-server|openid-configuration)$/.test(path)) return withClientCredentials(response, res, responseHeaders)
    res.writeHead(response.statusCode, responseHeaders)
    response.pipe(res)
  })
  upstream.on('error', dropped)
  return upstream
}

function proxy(req, res, path, upstreamPort, bodyBytes, options) {
  const upstream = upstreamRequest(req, res, path, upstreamPort, forwardHeaders(req.headers), options)
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

// observed reads GoTrue's answer, bounded by the request body bound, lets
// observe see its status and JSON, and relays it unchanged.
function observed(response, res, headers, observe) {
  const chunks = []
  let size = 0
  response.on('data', chunk => { size += chunk.length; if (size <= limits.bodyBytes) chunks.push(chunk) })
  response.on('end', async () => {
    if (response.aborted) return
    if (size > limits.bodyBytes) return problem(res, 502, 'auth_unavailable')
    const payload = Buffer.concat(chunks)
    let answer = null
    try { answer = JSON.parse(payload.toString('utf8')) } catch { /* not JSON: neither a failure nor a success to count */ }
    await observe(response.statusCode, answer)
    delete headers['content-length']
    res.writeHead(response.statusCode, { ...headers, 'content-length': payload.length })
    res.end(payload)
  })
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

// withClientCredentials adds the grant the front answers for service
// accounts to GoTrue's RFC 8414 metadata.
function withClientCredentials(response, res, headers) {
  const chunks = []
  let size = 0
  response.on('data', chunk => { size += chunk.length; if (size <= limits.authzBodyBytes) chunks.push(chunk) })
  response.on('end', () => {
    try {
      if (size > limits.authzBodyBytes) throw new Error('too large')
      const metadata = JSON.parse(Buffer.concat(chunks).toString('utf8'))
      const grants = Array.isArray(metadata.grant_types_supported) ? metadata.grant_types_supported : ['authorization_code', 'implicit']
      const payload = JSON.stringify({ ...metadata, grant_types_supported: [...new Set([...grants, 'client_credentials'])] })
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
  const tokenHook = config.oauth || config.orgClaims || config.impersonation
  const accessToken = tokenHook ? payload => accessTokenClaims(pool, payload, { orgClaims: config.orgClaims, impersonation: config.impersonation }) : null
  // Sign-up restrictions, and organizations' own identity providers, which
  // vouch only for their verified domains, need the before-user-created hook;
  // providers stay connected after AUTH_ORG_SSO is turned off, so it runs
  // whenever SAML does.
  const beforeUserCreated = config.signups || config.gotrueEnv.GOTRUE_SAML_ENABLED === 'true' ? payload => signupRefusal(pool, config.signups, payload) : null
  const hooks = createHookServer({ secret, mailer, api: config.api, app: config.app, accessToken, beforeUserCreated, templates: () => loadEmailTemplates(pool) })
  hooks.listen(hookAddress.port, hookAddress.host)
  const child = spawn(gotrue.binary, ['serve'], { env: { ...config.gotrueEnv, ...hookEnv(secret, { accessToken: tokenHook, beforeUserCreated: Boolean(beforeUserCreated) }), GOMEMLIMIT: goMemoryLimit() }, stdio: ['ignore', 'pipe', 'pipe'] })
  filterLogs(child.stdout, process.stdout)
  filterLogs(child.stderr, process.stderr)
  const invitations = { mailer, external: config.external, app: config.app }
  const ui = { siteURL: config.gotrueEnv.GOTRUE_SITE_URL, redirectURLs: config.redirects, app: config.app, branding: config.branding, captcha: config.captcha, web3: config.web3 }
  const workflows = workflowsConfig(env)
  const goTrue = goTrueAdmin(signingKey(config.gotrueEnv.GOTRUE_JWT_KEYS))
  const orgSSO = config.orgSSO ? { admin: goTrue, goTrueError, external: config.external } : null
  const deleteAccount = config.selfDeletion ? claims => deleteOwnAccount({ pool, admin: goTrue }, claims) : null
  // Service accounts' tokens are signed and checked here, with the app's keys.
  const jwtKeys = config.gotrueEnv.GOTRUE_JWT_KEYS
  const authenticate = config.serviceAccounts ? createAuthenticator(goTrueAuthenticator(), token => verifyToken(token, { keys: verificationKeys(jwtKeys), issuer: config.api })) : goTrueAuthenticator()
  // Failed secrets count per client address in the database, so instances
  // and wakes share the count; tokens per account stay per instance.
  const attempts = createAttempts(pool)
  const secretFailures = address => [{ key: budgetKey('service-account-client', addressBucket(address)), budget: budgets.serviceAccountClient }]
  const tokenLimits = { limiter: createRateLimiter(), failures: { check: address => attempts.check(secretFailures(address)), take: address => attempts.fail(secretFailures(address)) } }
  const serviceAccounts = config.serviceAccounts ? { issue: request => issueToken({ pool, signer: signingKey(jwtKeys), issuer: config.api, ...tokenLimits }, request) } : null
  const authz = createAuthzHandler({ pool, authenticate, origins: config.origins, invitations, storage: storageBindings(env), workflows: workflows && createWorkflowStarter(workflows), orgSSO, orgDomains: config.orgDomains, teamEnabled: config.productControlTeamEnabled, emailEnabled: config.productControlEmailEnabled, deleteAccount, orgClaims: config.orgClaims, sessions: config.sessions, serviceAccounts: config.serviceAccounts })
  const realtime = createRealtimeAuthorizer({ pool, tokens: callbackTokens(env) })
  // The dashboard is served from the Gregale API origin.
  let dashboard = []
  try { dashboard = [new URL(env.AUTH_GREGALE_API_URL).origin] } catch { /* no dashboard origin sealed */ }
  const adminKeys = adminKeyHashes(env)
  const admin = createAdminHandler({ pool, admin: goTrue, keys: adminKeys, origins: dashboard, external: config.external, emails: { mailer, api: config.api, app: config.app },
    impersonation: config.impersonation ? { siteURL: config.gotrueEnv.GOTRUE_SITE_URL } : null, serviceAccounts: config.serviceAccounts })
  const scim = createScimHandler({ pool, admin: goTrue, base: config.external })
  const oauth = config.oauth ? {
    location: location => consentLocation(location, config.gotrueEnv.GOTRUE_SITE_URL, config.external),
    registeredClients: () => registeredClients(pool),
    resource: id => authorizationResource(pool, id),
    clientCredentials: config.serviceAccounts,
  } : null
  const signIns = createSignInSecurity({ pool, mailer, alerts: config.security.newDeviceAlerts, app: config.app })
  const securityLog = createSecurityLog({ pool, secret: config.gotrueEnv.GOTRUE_JWT_SECRET })
  const server = createServer({ authz, ui, realtime, admin, // Admin keys are not listed in the public settings; the CLI asks
    // /settings/v1/admin-keys/<ID> for each key it finds among the secrets.
    settings: config.settings, adminKeys: adminKeySummary(adminKeys), smsCountries: config.smsCountries, scim, oauth, serviceAccounts, attempts,
    // Trusted networks and new-device alerts, and message budgets.
    signIns, sends: createSends({ attempts, pool, budgets: messageBudgets(config.security), trusted: signIns.trusted }), captcha: config.captcha !== null, securityLog })
  // Lifecycle events publish after requests that may change users, and
  // once at startup for anything recorded while the app was parked.
  const lifecycle = lifecycleConfig(env)
  const events = lifecycle && createEventPublisher({ pool, config: lifecycle })
  setLifecycleEvents(pool, Boolean(lifecycle)).then(() => events?.poke(), err => {
    console.error(JSON.stringify({ level: 'error', event: 'lifecycle_events_setup_failed', sqlstate: err?.code ?? null }))
  })
  // Joining by email domain happens in triggers on GoTrue's tables, so
  // the database learns at startup whether addresses here are proven.
  if (config.orgDomains && !config.domainJoin) console.error(JSON.stringify({ level: 'warning', event: 'domain_join_needs_confirmed_addresses' }))
  setDomainJoin(pool, config.domainJoin).catch(err => {
    console.error(JSON.stringify({ level: 'error', event: 'domain_join_setup_failed', sqlstate: err?.code ?? null }))
  })
  // Activity retention prunes on the same request-driven schedule.
  const pruners = [createAuditPruner({ pool, days: config.auditRetentionDays }), createOrgAuditPruner({ pool, days: config.auditRetentionDays }), createAnonymousPruner({ pool, days: config.anonymousRetentionDays }), createAttemptPruner({ pool }), createSignInSeenPruner({ pool }), createSecurityEventPruner({ pool, days: config.security.logDays }),
    ...(config.oauth ? [createUnusedClientPruner({ pool })] : [])]
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
      // Security events still queued get a moment to be written.
      server.close(() => Promise.race([securityLog.settled(), new Promise(resolve => setTimeout(resolve, 2000))]).finally(() => process.exit(0)))
    })
  }
  server.listen(Number(env.PORT ?? 8080))
  return { server, child }
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) start()
