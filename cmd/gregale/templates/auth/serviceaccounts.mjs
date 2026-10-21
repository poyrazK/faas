import { createHash, createPublicKey, randomBytes, randomUUID, sign, timingSafeEqual, verify } from 'node:crypto'
import { HTTPError } from './authz.mjs'
import { limits } from './config.mjs'

// Service accounts (ADR-1190, AUTH_SERVICE_ACCOUNTS): an organization's
// machine principals, in sql/0021. A backend trades the account's ID and a
// secret for an access token with RFC 6749's client-credentials grant at the
// OAuth token endpoint; the front answers that grant itself, since GoTrue
// has neither the grant nor the principal, and signs the token with the
// app's signing key. The token verifies like a person's (issuer, audience
// authenticated), so the Data API, edge rules and the SDKs accept it, and it
// is marked service_account, which the SQL helpers check against the
// account and the secret it came from on every authorize().

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
const permission = /^[a-z][a-z0-9_.:-]{0,63}$/
export const audience = 'authenticated'

export function newSecret() {
  const secret = `gasa_${randomBytes(32).toString('base64url')}`
  return { secret, hash: secretHash(secret), hint: secret.slice(-4) }
}

export function secretHash(secret) {
  return createHash('sha256').update(secret).digest()
}

// isServiceAccountToken reads unverified claims; it only ever narrows what
// a request may do.
export function isServiceAccountToken(claims) {
  return claims?.service_account === true
}

// serviceAccountAllowed lists what a service account's token may call on
// the Auth host: the permission check and storage URLs, both decided by
// authorize(). Everything else, GoTrue's user endpoints included, is for
// people.
export function serviceAccountAllowed(method, url) {
  return method === 'POST' && (url === '/authz/v1/authorize' || url === '/authz/v1/storage/sign')
}

// OAuthError is an RFC 6749 section 5.2 error response.
export class OAuthError extends Error {
  constructor(status, error, headers = {}) { super(error); this.status = status; this.error = error; this.headers = headers }
}

// tokenRequest reads a token request body; it returns null for any grant
// but client_credentials, which GoTrue then answers. The client
// authenticates with HTTP Basic (form-encoded ID and secret) or in the body,
// never both.
export function tokenRequest(contentType, body, authorization) {
  let fields
  if (/^application\/x-www-form-urlencoded(;|$)/i.test(contentType ?? '')) fields = Object.fromEntries(new URLSearchParams(body.toString('utf8')))
  else if (/^application\/json(;|$)/i.test(contentType ?? '')) {
    try { fields = JSON.parse(body.toString('utf8') || '{}') } catch { return null }
    if (fields === null || typeof fields !== 'object' || Array.isArray(fields)) return null
  } else return null
  if (fields.grant_type !== 'client_credentials') return null
  const basic = /^Basic ([A-Za-z0-9+/]+={0,2})$/i.exec(authorization ?? '')
  const inBody = fields.client_id !== undefined || fields.client_secret !== undefined
  if (authorization && !basic) throw new OAuthError(401, 'invalid_client')
  if (basic && inBody) throw new OAuthError(400, 'invalid_request')
  let clientId = fields.client_id, secret = fields.client_secret
  if (basic) {
    const decoded = Buffer.from(basic[1], 'base64').toString('utf8')
    const colon = decoded.indexOf(':')
    try {
      clientId = decodeURIComponent(decoded.slice(0, colon).replace(/\+/g, ' '))
      secret = decodeURIComponent(decoded.slice(colon + 1).replace(/\+/g, ' '))
    } catch { clientId = undefined }
    if (colon < 0) clientId = undefined
  }
  const challenge = basic ? { 'WWW-Authenticate': 'Basic realm="service-accounts"' } : {}
  if (typeof clientId !== 'string' || !uuid.test(clientId) || typeof secret !== 'string' || !/^gasa_[A-Za-z0-9_-]{43}$/.test(secret)) throw new OAuthError(401, 'invalid_client', challenge)
  let scope = null
  if (fields.scope !== undefined) {
    if (typeof fields.scope !== 'string') throw new OAuthError(400, 'invalid_scope')
    const requested = fields.scope.split(' ').filter(Boolean)
    if (requested.length === 0 || requested.length > 64 || !requested.every(item => permission.test(item))) throw new OAuthError(400, 'invalid_scope')
    scope = [...new Set(requested)].sort()
  }
  return { clientId: clientId.toLowerCase(), secret, scope, challenge }
}

// verificationKeys are the public halves of every key in GOTRUE_JWT_KEYS,
// by key ID: the signer, a standby key and previous keys.
export function verificationKeys(keys) {
  const out = new Map()
  for (const jwk of JSON.parse(keys)) {
    if (!Array.isArray(jwk.key_ops) || !jwk.key_ops.includes('verify')) continue
    out.set(jwk.kid, createPublicKey({ key: { kty: jwk.kty, crv: jwk.crv, x: jwk.x, y: jwk.y }, format: 'jwk' }))
  }
  return out
}

const encode = value => Buffer.from(JSON.stringify(value)).toString('base64url')

// mintToken signs a service account's access token. Its claims mirror a
// person's token with organization claims: org_permissions are what the
// token may use, the scope when one was asked for.
export function mintToken({ signer, issuer }, account, scope, now = Date.now()) {
  const iat = Math.floor(now / 1000)
  const claims = {
    iss: issuer, aud: audience, sub: account.id, role: 'authenticated', iat, exp: iat + limits.accessTokenSeconds, jti: randomUUID(),
    service_account: true, secret_id: account.secret_id,
    org_id: account.org_id, org_slug: account.org_slug, org_role: account.role, org_permissions: scope ?? account.permissions,
    ...(scope ? { scope: scope.join(' ') } : {}),
  }
  const unsigned = `${encode({ alg: 'ES256', typ: 'JWT', kid: signer.kid })}.${encode(claims)}`
  return `${unsigned}.${sign('sha256', Buffer.from(unsigned), { key: signer.key, dsaEncoding: 'ieee-p1363' }).toString('base64url')}`
}

// verifyToken checks a service account's token the way the Data API does:
// signature, issuer, audience and expiry. Whether the account and secret
// still hold is for the SQL helpers, at every check.
export function verifyToken(token, { keys, issuer }, now = Date.now()) {
  const parts = typeof token === 'string' ? token.split('.') : []
  let header, claims
  try { header = JSON.parse(Buffer.from(parts[0], 'base64url')); claims = JSON.parse(Buffer.from(parts[1], 'base64url')) } catch { throw new HTTPError(401, 'not_authenticated') }
  const key = header?.alg === 'ES256' && typeof header.kid === 'string' ? keys.get(header.kid) : undefined
  if (parts.length !== 3 || !key || !verify('sha256', Buffer.from(`${parts[0]}.${parts[1]}`), { key, dsaEncoding: 'ieee-p1363' }, Buffer.from(parts[2], 'base64url'))) throw new HTTPError(401, 'not_authenticated')
  if (claims === null || typeof claims !== 'object') throw new HTTPError(401, 'not_authenticated')
  const aud = Array.isArray(claims.aud) ? claims.aud : [claims.aud]
  if (claims.iss !== issuer || !aud.includes(audience) || typeof claims.exp !== 'number' || claims.exp * 1000 <= now) throw new HTTPError(401, 'not_authenticated')
  if (claims.service_account !== true || !uuid.test(claims.sub ?? '') || !uuid.test(claims.secret_id ?? '')) throw new HTTPError(401, 'not_authenticated')
  return claims
}

// createAuthenticator verifies a service account's token here and passes
// anyone else's to GoTrue.
export function createAuthenticator(people, verifier) {
  return token => {
    let claims = null
    try { claims = JSON.parse(Buffer.from(token.split('.')[1], 'base64url')) } catch { /* GoTrue refuses it */ }
    return isServiceAccountToken(claims) ? verifier(token) : people(token)
  }
}

// createRateLimiter counts requests per key in each minute: take counts
// one and check only looks; both return the seconds to wait, or 0. The map
// is bounded so made-up keys cannot grow it without end.
export function createRateLimiter(perMinute = limits.serviceAccountTokensPerMinute, now = Date.now, size = 10000) {
  const windows = new Map()
  const entry = id => {
    const minute = Math.floor(now() / 60000)
    let current = windows.get(id)
    if (!current || current.minute !== minute) {
      if (!current && windows.size >= size) windows.clear()
      current = { minute, count: 0 }
      windows.set(id, current)
    }
    return current
  }
  const wait = count => count < perMinute ? 0 : 60 - Math.floor(now() / 1000) % 60
  return {
    take: id => { const current = entry(id); current.count++; return wait(current.count - 1) },
    check: id => wait(windows.get(id)?.minute === Math.floor(now() / 60000) ? windows.get(id).count : 0),
  }
}

// issueToken answers a client-credentials request. A wrong ID or secret,
// or a disabled account, is invalid_client either way. The token path
// bypasses GoTrue's rate limits, so it keeps two of its own: failed
// requests per client address, checked before the database is asked, and
// tokens per account, counted only once the secret matched, so knowing an
// account's ID is not enough to exhaust its budget.
export async function issueToken({ pool, signer, issuer, limiter, failures }, request) {
  const blocked = failures.check(request.address)
  if (blocked > 0) throw new OAuthError(429, 'slow_down', { 'Retry-After': String(blocked) })
  const presented = secretHash(request.secret)
  const { rows } = await pool.query('SELECT secret_id, secret_hash, org_id, org_slug, role, permissions FROM gregale_auth.service_account_credentials($1)', [request.clientId])
  // Compare with every live secret so timing does not tell which matched.
  const match = rows.reduce((found, row) => timingSafeEqual(row.secret_hash, presented) ? row : found, null)
  if (!match) {
    failures.take(request.address)
    throw new OAuthError(401, 'invalid_client', request.challenge)
  }
  const wait = limiter.take(request.clientId)
  if (wait > 0) throw new OAuthError(429, 'slow_down', { 'Retry-After': String(wait) })
  if (request.scope && !match.permissions.includes('*') && !request.scope.every(item => match.permissions.includes(item))) throw new OAuthError(400, 'invalid_scope')
  await pool.query(`UPDATE gregale_auth.service_account_secrets SET last_used_at = now()
    WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < now() - interval '1 minute')`, [match.secret_id])
  const token = mintToken({ signer, issuer }, { id: request.clientId, ...match }, request.scope)
  return { access_token: token, token_type: 'Bearer', expires_in: limits.accessTokenSeconds, ...(request.scope ? { scope: request.scope.join(' ') } : {}) }
}

// accountInput checks a create or change request's fields.
function accountInput(body, creating) {
  if (body === null || typeof body !== 'object' || Array.isArray(body)) throw new HTTPError(400, 'invalid_service_account')
  const out = { name: null, role: null, disabled: null, days: null }
  for (const [field, value] of Object.entries(body)) {
    if (field === 'name') {
      if (typeof value !== 'string' || value.trim().length < 1 || value.trim().length > 100 || /[\x00-\x1f\x7f]/.test(value)) throw new HTTPError(400, 'invalid_name')
      out.name = value
    } else if (field === 'role') {
      if (typeof value !== 'string' || !/^[a-z][a-z0-9_-]{0,31}$/.test(value)) throw new HTTPError(400, 'invalid_role')
      out.role = value
    } else if (field === 'disabled' && !creating) {
      if (typeof value !== 'boolean') throw new HTTPError(400, 'invalid_disabled')
      out.disabled = value
    } else if (field === 'expires_in_days' && creating) {
      out.days = secretDays(body)
    } else if (!(field === 'org' && creating)) throw new HTTPError(400, 'invalid_service_account')
  }
  if (creating && (out.name === null || out.role === null)) throw new HTTPError(400, out.name === null ? 'invalid_name' : 'invalid_role')
  if (!creating && out.name === null && out.role === null && out.disabled === null) throw new HTTPError(400, 'invalid_service_account')
  return out
}

function secretDays(body) {
  const days = body?.expires_in_days ?? null
  if (days !== null && (!Number.isInteger(days) || days < 1 || days > limits.serviceAccountSecretMaxDays)) throw new HTTPError(400, 'invalid_expires_in_days')
  return days
}

const accountColumns = 'id, org_id, name, role, disabled_at IS NOT NULL AS disabled, created_at'

// created is a new account's or secret's answer: the secret is shown once.
function created(row, secret) {
  return { client_id: row.service_account_id ?? row.id, client_secret: secret.secret, secret: { id: row.secret_id ?? row.id, hint: secret.hint, expires_at: row.expires_at } }
}

// organizationRoute serves /authz/v1/orgs/<org>/service-accounts/... for
// members holding members:manage; the SQL functions check that and the
// owner rule. parts follow "service-accounts".
export async function organizationRoute(rows, org, method, parts, body) {
  if (parts.length === 0 && method === 'GET') {
    return [200, { service_accounts: await rows('SELECT id, name, role, disabled, created_at, secrets FROM gregale_auth.list_service_accounts($1)', [org]) }]
  }
  if (parts.length === 0 && method === 'POST') {
    const input = accountInput(body, true)
    const secret = newSecret()
    const [row] = await rows('SELECT * FROM gregale_auth.create_service_account($1, $2, $3, $4, $5, $6)', [org, input.name, input.role, secret.hash, secret.hint, input.days])
    return [201, { id: row.id, org_id: row.org_id, name: row.name, role: row.role, created_at: row.created_at, ...created(row, secret) }]
  }
  const account = parts[0]
  if (!uuid.test(account ?? '')) throw new HTTPError(400, 'invalid_id')
  if (parts.length === 1 && method === 'PATCH') {
    const input = accountInput(body, false)
    const [row] = await rows(`SELECT ${accountColumns} FROM gregale_auth.update_service_account($1, $2, $3, $4, $5)`, [org, account, input.name, input.role, input.disabled])
    return [200, row]
  }
  if (parts.length === 1 && method === 'DELETE') {
    await rows('SELECT gregale_auth.delete_service_account($1, $2)', [org, account])
    return [204, null]
  }
  if (parts[1] === 'secrets' && parts.length === 2 && method === 'POST') {
    const secret = newSecret()
    const [row] = await rows('SELECT id, service_account_id, expires_at FROM gregale_auth.create_service_account_secret($1, $2, $3, $4, $5)', [org, account, secret.hash, secret.hint, secretDays(body)])
    return [201, created(row, secret)]
  }
  if (parts[1] === 'secrets' && parts.length === 3 && method === 'DELETE') {
    if (!uuid.test(parts[2])) throw new HTTPError(400, 'invalid_id')
    await rows('SELECT gregale_auth.delete_service_account_secret($1, $2, $3)', [org, account, parts[2]])
    return [204, null]
  }
  throw new HTTPError(404, 'not_found')
}

// adminRoute serves /admin/v1/service-accounts/... for the app's operator,
// in any organization; owner-role accounts too. parts follow
// "service-accounts". Changes carry no actor in the audit log.
export async function adminRoute(rows, method, parts, query, body) {
  if (parts.length === 0 && method === 'GET') {
    const org = query.get('org')
    if (org !== null && !uuid.test(org)) throw new HTTPError(400, 'invalid_id')
    return [200, { service_accounts: await rows('SELECT * FROM gregale_auth.service_account_rows($1, $2)', [org, limits.adminPageSize]) }]
  }
  if (parts.length === 0 && method === 'POST') {
    const input = accountInput(body, true)
    if (typeof body.org !== 'string' || !uuid.test(body.org)) throw new HTTPError(400, 'invalid_org')
    const secret = newSecret()
    const [row] = await rows('SELECT * FROM gregale_auth.insert_service_account($1, $2, $3, $4, $5, $6)', [body.org, input.name, input.role, secret.hash, secret.hint, input.days])
    return [201, { id: row.id, org_id: row.org_id, name: row.name, role: row.role, created_at: row.created_at, ...created(row, secret) }]
  }
  const account = parts[0]
  if (!uuid.test(account ?? '')) throw new HTTPError(400, 'invalid_id')
  if (parts.length === 1 && method === 'PATCH') {
    const input = accountInput(body, false)
    const [row] = await rows(`SELECT ${accountColumns} FROM gregale_auth.change_service_account($1, $2, $3, $4)`, [account, input.name, input.role, input.disabled])
    return [200, row]
  }
  if (parts.length === 1 && method === 'DELETE') {
    const deleted = await rows('DELETE FROM gregale_auth.service_accounts WHERE id = $1 RETURNING id', [account])
    if (deleted.length === 0) throw new HTTPError(404, 'not_found')
    return [204, null]
  }
  if (parts[1] === 'secrets' && parts.length === 2 && method === 'POST') {
    const secret = newSecret()
    const [row] = await rows('SELECT id, service_account_id, expires_at FROM gregale_auth.add_service_account_secret($1, $2, $3, $4)', [account, secret.hash, secret.hint, secretDays(body)])
    return [201, created(row, secret)]
  }
  if (parts[1] === 'secrets' && parts.length === 3 && method === 'DELETE') {
    if (!uuid.test(parts[2])) throw new HTTPError(400, 'invalid_id')
    await rows('SELECT gregale_auth.revoke_service_account_secret($1, $2)', [account, parts[2]])
    return [204, null]
  }
  throw new HTTPError(404, 'not_found')
}
