import { createHash, createPrivateKey, randomBytes, sign, timingSafeEqual } from 'node:crypto'
import http from 'node:http'
import { gotrue, limits } from './config.mjs'
import { HTTPError, readJSON } from './authz.mjs'
import { ssoRoute } from './sso.mjs'
import { oauthRoute } from './oauth.mjs'
import { emailRoute } from './emails.mjs'
import { signupsRoute } from './signups.mjs'

// /admin/v1: owner administration of an application Auth app (ADR-1190
// phase 6). The owner holds the key; the app stores only its SHA-256 as
// AUTH_ADMIN_KEY_<id>. The Gregale dashboard calls this API from the owner's
// browser, so user records never pass through or stay in Gregale's control
// plane. User changes go through GoTrue's own admin API on loopback, with a
// service_role token this module mints for one minute.

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

export function adminKeyHashes(env) {
  return Object.entries(env)
    .filter(([k, v]) => /^AUTH_ADMIN_KEY_[0-9A-F]{8}$/.test(k) && typeof v === 'string' && /^[0-9a-f]{64}$/.test(v))
    .map(([, v]) => Buffer.from(v, 'hex'))
}

export function newAdminKey() {
  const key = `gaak_${randomBytes(32).toString('base64url')}`
  return { key, hash: createHash('sha256').update(key).digest('hex') }
}

function keyMatches(hashes, header) {
  const key = /^Bearer (gaak_[A-Za-z0-9_-]{43})$/.exec(header ?? '')?.[1]
  if (!key) return false
  const presented = createHash('sha256').update(key).digest()
  // Compare against every hash so timing does not reveal which key matched.
  return hashes.reduce((found, hash) => timingSafeEqual(hash, presented) || found, false)
}

// signingKey picks the one ES256 key in GOTRUE_JWT_KEYS that signs. GoTrue
// accepts only its configured asymmetric methods, so HS256 is refused.
export function signingKey(keys) {
  const jwk = JSON.parse(keys).find(key => Array.isArray(key.key_ops) && key.key_ops.includes('sign'))
  return { kid: jwk.kid, key: createPrivateKey({ key: { kty: jwk.kty, crv: jwk.crv, x: jwk.x, y: jwk.y, d: jwk.d }, format: 'jwk' }) }
}

// serviceToken is accepted by GoTrue's admin API: role service_role, no
// session and no subject, valid for one minute.
export function serviceToken({ kid, key }, now = Date.now()) {
  const encode = value => Buffer.from(JSON.stringify(value)).toString('base64url')
  const iat = Math.floor(now / 1000)
  const unsigned = `${encode({ alg: 'ES256', typ: 'JWT', kid })}.${encode({ role: 'service_role', iat, exp: iat + 60 })}`
  return `${unsigned}.${sign('sha256', Buffer.from(unsigned), { key, dsaEncoding: 'ieee-p1363' }).toString('base64url')}`
}

// goTrueAdmin calls GoTrue's admin API on loopback.
export function goTrueAdmin(signer, port = gotrue.port) {
  return (method, path, body) => new Promise((resolve, reject) => {
    const payload = body === undefined ? undefined : JSON.stringify(body)
    const req = http.request({
      host: gotrue.host, port, method, path, timeout: 10000,
      headers: { Authorization: `Bearer ${serviceToken(signer)}`, ...(payload ? { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(payload) } : {}) },
    }, res => {
      const chunks = []
      let size = 0
      res.on('data', chunk => { size += chunk.length; if (size <= limits.adminBodyBytes) chunks.push(chunk) })
      res.on('end', () => {
        let data = null
        try { data = chunks.length ? JSON.parse(Buffer.concat(chunks).toString('utf8')) : null } catch { data = null }
        resolve({ status: res.statusCode, headers: res.headers, data })
      })
    })
    req.on('timeout', () => req.destroy())
    req.on('error', () => reject(new HTTPError(503, 'auth_unavailable')))
    req.end(payload)
  })
}

export function goTrueError({ status, data }) {
  if (status >= 500) return new HTTPError(503, 'auth_unavailable')
  if (status === 404) return new HTTPError(404, 'not_found')
  // GoTrue's error codes are stable identifiers; its messages are not echoed.
  const code = typeof data?.error_code === 'string' && /^[a-z0-9_]{1,64}$/.test(data.error_code) ? data.error_code : 'invalid_request'
  return new HTTPError(status === 422 ? 409 : 400, code)
}

// Only these fields leave the app; metadata stays in the database.
// Custom claims are the keys of a user's app_metadata other than GoTrue's
// own provider and providers. Only the admin API writes app_metadata, and
// GoTrue puts it in every access token, so a backend or row-level security
// reads them as app_metadata.<name> from the next token on.
const goTrueMetadata = new Set(['provider', 'providers'])
const claimName = /^[A-Za-z][A-Za-z0-9_]{0,63}$/

export function customClaims(appMetadata) {
  return Object.fromEntries(Object.entries(appMetadata ?? {}).filter(([name]) => !goTrueMetadata.has(name)))
}

// claimsChange checks {set, unset} against the user's current claims and
// returns GoTrue's merge patch (null removes a key) and the result.
export function claimsChange(current, body) {
  const plain = value => value !== null && typeof value === 'object' && !Array.isArray(value)
  if (!plain(body) || Object.keys(body).some(key => key !== 'set' && key !== 'unset')) throw new HTTPError(400, 'invalid_claims')
  const set = body.set ?? {}, unset = body.unset ?? []
  if (!plain(set) || !Array.isArray(unset) || (Object.keys(set).length === 0 && unset.length === 0)) throw new HTTPError(400, 'invalid_claims')
  for (const name of [...Object.keys(set), ...unset]) {
    if (typeof name !== 'string' || !claimName.test(name) || goTrueMetadata.has(name)) throw new HTTPError(400, 'invalid_claim_name')
  }
  if (Object.values(set).some(value => value === null) || unset.some(name => Object.hasOwn(set, name))) throw new HTTPError(400, 'invalid_claims')
  const next = { ...customClaims(current), ...set }
  for (const name of unset) delete next[name]
  if (Buffer.byteLength(JSON.stringify(next)) > limits.customClaimsBytes) throw new HTTPError(400, 'claims_too_large')
  return { patch: { ...set, ...Object.fromEntries(unset.map(name => [name, null])) }, next }
}

function userView(u) {
  return {
    id: u.id, email: u.email ?? null, phone: u.phone || null,
    created_at: u.created_at ?? null, last_sign_in_at: u.last_sign_in_at ?? null,
    email_confirmed_at: u.email_confirmed_at ?? null, banned_until: u.banned_until ?? null,
    providers: Array.isArray(u.app_metadata?.providers) ? u.app_metadata.providers.filter(p => typeof p === 'string') : [],
    anonymous: u.is_anonymous === true,
    claims: customClaims(u.app_metadata),
  }
}

function pageParams(query) {
  const page = Number(query.get('page') ?? 1)
  const perPage = Number(query.get('per_page') ?? 50)
  if (!Number.isInteger(page) || page < 1 || page > 100000) throw new HTTPError(400, 'invalid_page')
  if (!Number.isInteger(perPage) || perPage < 1 || perPage > limits.adminPageSize) throw new HTTPError(400, 'invalid_per_page')
  const search = query.get('search') ?? ''
  if (search.length > 200 || /[\x00-\x1f\x7f]/.test(search)) throw new HTTPError(400, 'invalid_search')
  return { page, perPage, search }
}

const likePattern = value => `%${value.replace(/[\\%_]/g, c => `\\${c}`)}%`

// A user who is the last owner of an organization with other members cannot
// be deleted: that organization would have no one able to manage it.
const lastOwnerSQL = `SELECT o.slug FROM gregale_auth.memberships m JOIN gregale_auth.organizations o ON o.id = m.org_id
  WHERE m.user_id = $1 AND m.role = 'owner'
    AND NOT EXISTS (SELECT 1 FROM gregale_auth.memberships x WHERE x.org_id = m.org_id AND x.user_id <> $1 AND x.role = 'owner')
    AND EXISTS (SELECT 1 FROM gregale_auth.memberships y WHERE y.org_id = m.org_id AND y.user_id <> $1)
  ORDER BY o.slug LIMIT 20`
const soleMemberSQL = `SELECT m.org_id FROM gregale_auth.memberships m
  WHERE m.user_id = $1 AND NOT EXISTS (SELECT 1 FROM gregale_auth.memberships x WHERE x.org_id = m.org_id AND x.user_id <> $1)`

// deleteUser removes a user through GoTrue's admin API and returns
// [status, body]. The last owner of an organization with other members is
// refused; organizations only this user belonged to would be unreachable,
// so they go too.
export async function deleteUser({ pool, admin }, user) {
  const { rows: blocked } = await pool.query(lastOwnerSQL, [user])
  if (blocked.length) return [409, { status: 409, code: 'last_owner', organizations: blocked.map(r => r.slug) }]
  const sole = (await pool.query(soleMemberSQL, [user])).rows.map(r => r.org_id)
  const response = await admin('DELETE', `/admin/users/${user}`)
  if (response.status !== 200) throw goTrueError(response)
  const { rowCount } = sole.length
    ? await pool.query('DELETE FROM gregale_auth.organizations o WHERE o.id = ANY($1::uuid[]) AND NOT EXISTS (SELECT 1 FROM gregale_auth.memberships x WHERE x.org_id = o.id)', [sole])
    : { rowCount: 0 }
  return [200, { deleted: true, organizations_deleted: rowCount }]
}

// deleteOwnAccount lets a signed-in person delete themselves (AUTH_SELF_DELETION).
// Their session must be recent and, if they have a second factor, have
// passed it; an anonymous user cannot sign in again, so only holds the
// session. claims come from a token GoTrue verified.
export async function deleteOwnAccount({ pool, admin }, claims) {
  const { rows: [session] } = await pool.query(`SELECT u.is_anonymous OR (s.created_at > now() - make_interval(secs => $3)
      AND (s.aal = 'aal2' OR NOT EXISTS (SELECT 1 FROM auth.mfa_factors f WHERE f.user_id = u.id AND f.status = 'verified' AND f.factor_type::text <> 'recovery_code'))) AS allowed
    FROM auth.sessions s JOIN auth.users u ON u.id = s.user_id WHERE s.id = $2 AND s.user_id = $1`, [claims.sub, claims.session_id, limits.reauthenticationSeconds])
  if (!session) throw new HTTPError(401, 'not_authenticated')
  if (!session.allowed) throw new HTTPError(403, 'reauthentication_required')
  return deleteUser({ pool, admin }, claims.sub)
}

const hashFormats = [/^\$2[aby]\$\d{2}\$[./A-Za-z0-9]{53}$/, /^\$argon2(id|i|d)\$v=(16|19)\$m=\d+,t=\d+,p=\d+(,[^$]*)?\$[^$]*\$[^$]+$/, /^\$fbscrypt\$v=\d+,n=\d+,r=\d+,p=\d+(,[^$]*)?\$[^$]+\$[^$]+$/]

// importRecord validates one user before it reaches GoTrue. Plain-text
// passwords are refused: an export file should never hold them.
export function importRecord(record) {
  if (!record || typeof record !== 'object' || Array.isArray(record)) throw new HTTPError(400, 'invalid_record')
  const allowed = new Set(['id', 'email', 'password_hash', 'email_confirmed', 'user_metadata', 'app_metadata'])
  if (Object.keys(record).some(k => !allowed.has(k))) throw new HTTPError(400, 'unknown_field')
  const { id, email, password_hash: hash, email_confirmed: confirmed, user_metadata: userMetadata, app_metadata: appMetadata } = record
  if (typeof email !== 'string' || email.length > 320 || !/^[^\s@]+@[^\s@]+$/.test(email)) throw new HTTPError(400, 'invalid_email')
  if (id !== undefined && (typeof id !== 'string' || !uuid.test(id))) throw new HTTPError(400, 'invalid_id')
  if (hash !== undefined && (typeof hash !== 'string' || hash.length > 512 || !hashFormats.some(f => f.test(hash)))) throw new HTTPError(400, 'unsupported_password_hash')
  if (confirmed !== undefined && typeof confirmed !== 'boolean') throw new HTTPError(400, 'invalid_email_confirmed')
  for (const [name, value] of [['user_metadata', userMetadata], ['app_metadata', appMetadata]]) {
    if (value !== undefined && (!value || typeof value !== 'object' || Array.isArray(value) || JSON.stringify(value).length > 16384)) throw new HTTPError(400, `invalid_${name}`)
  }
  return {
    email, email_confirm: confirmed === true,
    ...(id ? { id } : {}), ...(hash ? { password_hash: hash } : {}),
    ...(userMetadata ? { user_metadata: userMetadata } : {}), ...(appMetadata ? { app_metadata: appMetadata } : {}),
  }
}

async function importUsers(admin, users) {
  if (!Array.isArray(users) || users.length === 0 || users.length > limits.importBatch) throw new HTTPError(400, 'invalid_batch')
  let created = 0
  const skipped = []
  // GoTrue checks for an existing email before inserting, so two concurrent
  // creates of one address race; repeats within a batch are skipped here.
  const seen = new Set()
  const records = users.map((user, index) => {
    try {
      const record = importRecord(user)
      const keys = [`email:${record.email.toLowerCase()}`, ...(record.id ? [`id:${record.id.toLowerCase()}`] : [])]
      if (keys.some(key => seen.has(key))) throw new HTTPError(400, 'duplicate_in_batch')
      keys.forEach(key => seen.add(key))
      return { index, record }
    } catch (err) {
      skipped.push({ index, code: err.code })
      return null
    }
  }).filter(Boolean)
  let next = 0
  const worker = async () => {
    while (next < records.length) {
      const { index, record } = records[next++]
      try {
        const response = await admin('POST', '/admin/users', record)
        if (response.status === 200 || response.status === 201) created++
        else skipped.push({ index, code: goTrueError(response).code })
      } catch (err) {
        if (err instanceof HTTPError && err.status === 503) throw err
        skipped.push({ index, code: err instanceof HTTPError ? err.code : 'invalid_record' })
      }
    }
  }
  await Promise.all(Array.from({ length: Math.min(4, records.length) }, worker))
  return { created, skipped: skipped.sort((a, b) => a.index - b.index) }
}

// activity reads GoTrue's audit log (sign-ins, sign-ups, password and
// account changes), newest first, overall or for one user. search matches an
// action exactly or an address in part. Pages carry no total, which would
// count the whole log. GoTrue v2.197.0 leaves the log's IP address empty, so
// none is returned. GoTrue records administrator actions (this API acting
// as service_role) with the zero actor ID and the affected user in traits;
// they are reported against that user, marked as an administrator's.
const administratorID = '00000000-0000-0000-0000-000000000000'
async function activity(query, params) {
  const { page, perPage, search } = pageParams(params)
  const user = params.get('user') ?? ''
  if (user && !uuid.test(user)) throw new HTTPError(400, 'invalid_id')
  const rows = await query(`SELECT id, created_at, payload->>'action' AS action,
      CASE WHEN payload->>'actor_id' = $6 THEN 'administrator' ELSE 'user' END AS actor,
      COALESCE(NULLIF(payload->'traits'->>'user_id', ''), NULLIF(payload->>'actor_id', $6)) AS user_id,
      COALESCE(NULLIF(payload->'traits'->>'user_email', ''), CASE WHEN payload->>'actor_id' = $6 THEN NULL ELSE payload->>'actor_username' END) AS email
    FROM auth.audit_log_entries
    WHERE ($1 = '' OR payload->>'actor_id' = $1 OR payload->'traits'->>'user_id' = $1)
      AND ($2 = '' OR payload->>'action' = $2 OR payload->>'actor_username' ILIKE $3 ESCAPE '\\' OR payload->'traits'->>'user_email' ILIKE $3 ESCAPE '\\')
    ORDER BY created_at DESC, id LIMIT $4 OFFSET $5`, [user, search, likePattern(search), perPage + 1, (page - 1) * perPage, administratorID])
  return { entries: rows.slice(0, perPage), more: rows.length > perPage, page, per_page: perPage }
}

// adminRoute returns [status, body] for one authorized request.
export async function adminRoute({ pool, admin, external, emails = {} }, method, url, body) {
  const path = url.pathname.replace(/^\/admin\/v1/, '')
  const parts = path.split('/').filter(Boolean)
  const query = async (sql, args) => (await pool.query(sql, args)).rows
  if (parts[0] === 'sso') return ssoRoute({ query, admin, goTrueError, external }, method, parts, body)
  if (parts[0] === 'oauth') return oauthRoute({ admin, goTrueError }, method, parts, body)
  if (parts[0] === 'emails') return emailRoute({ pool, external, ...emails }, method, parts, body)
  if (parts[0] === 'signups') return signupsRoute({ pool, mailer: emails.mailer, external, app: emails.app, pageParams, likePattern }, method, parts, url, body)
  const user = parts[0] === 'users' && parts[1] && parts[1] !== 'import' ? (uuid.test(parts[1]) ? parts[1] : null) : undefined
  if (user === null) throw new HTTPError(400, 'invalid_id')

  if (path === '/users' && method === 'GET') {
    const { page, perPage, search } = pageParams(url.searchParams)
    const qs = new URLSearchParams({ page: String(page), per_page: String(perPage), ...(search ? { filter: search } : {}) })
    const response = await admin('GET', `/admin/users?${qs}`)
    if (response.status !== 200) throw goTrueError(response)
    const total = Number(response.headers['x-total-count'])
    return [200, { users: (response.data?.users ?? []).map(userView), total: Number.isFinite(total) ? total : null, page, per_page: perPage }]
  }
  if (path === '/users/import' && method === 'POST') return [200, await importUsers(admin, body?.users)]
  if (user && parts.length === 2 && method === 'GET') {
    const response = await admin('GET', `/admin/users/${user}`)
    if (response.status !== 200) throw goTrueError(response)
    const [organizations, [{ sessions }]] = await Promise.all([
      query('SELECT o.id, o.slug, o.name, m.role FROM gregale_auth.memberships m JOIN gregale_auth.organizations o ON o.id = m.org_id WHERE m.user_id = $1 ORDER BY o.slug LIMIT 500', [user]),
      query('SELECT count(*)::int AS sessions FROM auth.sessions WHERE user_id = $1 AND (not_after IS NULL OR not_after > now())', [user]),
    ])
    return [200, { user: userView(response.data), organizations, active_sessions: sessions }]
  }
  if (user && parts[2] === 'claims' && parts.length === 3 && method === 'PATCH') {
    const current = await admin('GET', `/admin/users/${user}`)
    if (current.status !== 200) throw goTrueError(current)
    const { patch } = claimsChange(current.data?.app_metadata, body)
    const response = await admin('PUT', `/admin/users/${user}`, { app_metadata: patch })
    if (response.status !== 200) throw goTrueError(response)
    return [200, { claims: customClaims(response.data?.app_metadata) }]
  }
  if (user && parts[2] === 'sign-out' && parts.length === 3 && method === 'POST') {
    // GoTrue has no admin sign-out; deleting sessions ends their refresh
    // tokens, and every authorize() check fails from the next request.
    const { rowCount } = await pool.query('DELETE FROM auth.sessions WHERE user_id = $1', [user])
    return [200, { sessions_ended: rowCount }]
  }
  if (user && parts[2] === 'ban' && parts.length === 3 && method === 'POST') {
    const duration = body?.duration
    if (typeof duration !== 'string' || !/^([1-9]\d{0,5}h|none)$/.test(duration)) throw new HTTPError(400, 'invalid_duration')
    const response = await admin('PUT', `/admin/users/${user}`, { ban_duration: duration })
    if (response.status !== 200) throw goTrueError(response)
    // A ban stops refresh; ending sessions also stops checks at once.
    if (duration !== 'none') await pool.query('DELETE FROM auth.sessions WHERE user_id = $1', [user])
    return [200, { user: userView(response.data) }]
  }
  if (user && parts.length === 2 && method === 'DELETE') return deleteUser({ pool, admin }, user)
  if (path === '/activity' && method === 'GET') return [200, await activity(query, url.searchParams)]
  if (path === '/orgs' && method === 'GET') {
    const { page, perPage, search } = pageParams(url.searchParams)
    const rows = await query(`SELECT o.id, o.slug, o.name, o.created_at, (SELECT count(*)::int FROM gregale_auth.memberships m WHERE m.org_id = o.id) AS members, count(*) OVER ()::int AS total
      FROM gregale_auth.organizations o WHERE $1 = '' OR o.slug ILIKE $2 ESCAPE '\\' OR o.name ILIKE $2 ESCAPE '\\'
      ORDER BY o.created_at DESC, o.id LIMIT $3 OFFSET $4`, [search, likePattern(search), perPage, (page - 1) * perPage])
    return [200, { organizations: rows.map(({ total, ...org }) => org), total: rows[0]?.total ?? 0, page, per_page: perPage }]
  }
  if (parts[0] === 'orgs' && parts.length === 2 && method === 'GET') {
    if (!uuid.test(parts[1])) throw new HTTPError(400, 'invalid_id')
    const [org] = await query('SELECT id, slug, name, created_at FROM gregale_auth.organizations WHERE id = $1', [parts[1]])
    if (!org) throw new HTTPError(404, 'not_found')
    const [members, invitations] = await Promise.all([
      query('SELECT m.user_id, u.email, m.role, m.created_at FROM gregale_auth.memberships m JOIN auth.users u ON u.id = m.user_id WHERE m.org_id = $1 ORDER BY m.created_at LIMIT 500', [org.id]),
      query('SELECT id, email, role, expires_at, created_at FROM gregale_auth.invitations WHERE org_id = $1 AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > now() ORDER BY created_at DESC LIMIT 500', [org.id]),
    ])
    return [200, { organization: org, members, invitations }]
  }
  throw new HTTPError(404, 'not_found')
}

// createAdminHandler serves /admin/v1. Without a key it does not exist.
export function createAdminHandler({ pool, admin, keys, origins = [], external, emails = {} }) {
  const allowed = new Set(origins)
  return async (req, res) => {
    const headers = { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' }
    const origin = req.headers.origin
    if (origin && allowed.has(origin)) Object.assign(headers, { 'Access-Control-Allow-Origin': origin, Vary: 'Origin' })
    const reply = (status, payload) => { res.writeHead(status, headers); res.end(payload === undefined ? undefined : JSON.stringify(payload)) }
    if (keys.length === 0) { req.resume(); return reply(404, { status: 404, code: 'not_found' }) }
    if (req.method === 'OPTIONS') {
      if (!origin || !allowed.has(origin)) return reply(403, { status: 403, code: 'origin_not_allowed' })
      res.writeHead(204, { ...headers, 'Access-Control-Allow-Methods': 'GET,POST,PATCH,DELETE,OPTIONS', 'Access-Control-Allow-Headers': 'Authorization,Content-Type', 'Access-Control-Max-Age': '600' })
      return res.end()
    }
    try {
      if (!keyMatches(keys, req.headers.authorization)) { req.resume(); throw new HTTPError(401, 'invalid_admin_key') }
      const url = new URL(req.url, 'http://admin.invalid')
      const body = await readJSON(req, limits.adminBodyBytes)
      const [status, payload] = await adminRoute({ pool, admin, external, emails }, req.method, url, body)
      reply(status, payload)
    } catch (err) {
      const failure = err instanceof HTTPError ? err : new HTTPError(500, 'internal_error')
      if (failure.status >= 500) console.error(JSON.stringify({ level: 'error', event: 'admin_failed', code: failure.code, sqlstate: err?.code ?? null }))
      if (res.headersSent) return res.destroy()
      reply(failure.status, { status: failure.status, code: failure.code })
    }
  }
}
