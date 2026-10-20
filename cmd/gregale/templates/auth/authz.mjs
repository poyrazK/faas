import { createHash, randomBytes } from 'node:crypto'
import http from 'node:http'
import { gotrue, limits } from './config.mjs'
import { EmailError, invitationMessage, loadEmailTemplates } from './email.mjs'
import { objectResource, objectURL, presign } from './storage.mjs'
import { WorkflowError } from './workflows.mjs'
import { routeTeam, TeamError } from './team.mjs'
import { emailPath, emailConsent, resolveEmailRecipient, ProductEmailError } from './product-email.mjs'
import { domainJoinRoute, orgSSORoute } from './orgsso.mjs'

// /authz/v1: organizations, members, invitations and authorization checks
// (ADR-1190). Every operation is a gregale_auth SQL function that enforces
// its own permission rules; this module authenticates the caller, sets
// request.jwt.claims for one transaction and maps stable errors to HTTP.

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
const codes = new Map([
  ['28000', 401], ['42501', 403], ['P0002', 404], ['23505', 409], ['23514', 409], ['23503', 409], ['54000', 409], ['22023', 400], ['22P02', 400],
])
// Messages raised by gregale_auth functions; anything else is never echoed.
const stableMessages = new Set(['not_authenticated', 'forbidden', 'not_found', 'slug_taken', 'invalid_organization', 'organization_limit', 'anonymous_user',
  'unknown_role', 'invalid_invitation', 'invitation_limit', 'invitation_invalid', 'invitation_email_mismatch', 'already_member', 'last_owner',
  'domain_taken', 'domain_limit', 'invalid_domain', 'domain_in_use', 'domain_not_verified', 'sso_provider_limit', 'team_limit', 'current_session', 'mfa_required',
  'invalid_join_mode'])

export class HTTPError extends Error {
  constructor(status, code) { super(code); this.status = status; this.code = code }
}

export function hashToken(token) {
  if (typeof token !== 'string' || !/^[A-Za-z0-9_-]{43}$/.test(token)) throw new HTTPError(400, 'invalid_token')
  return createHash('sha256').update(Buffer.from(token, 'base64url')).digest()
}

export function newInvitationToken() {
  const token = randomBytes(32).toString('base64url')
  return { token, hash: hashToken(token) }
}

function claimsFromToken(token, user) {
  // GoTrue has already verified this token; read only its session claim.
  let payload
  try { payload = JSON.parse(Buffer.from(token.split('.')[1], 'base64url').toString('utf8')) } catch { throw new HTTPError(401, 'not_authenticated') }
  if (payload.sub !== user.id || typeof payload.session_id !== 'string') throw new HTTPError(401, 'not_authenticated')
  return { sub: user.id, session_id: payload.session_id, ...(payload.client_id ? { client_id: String(payload.client_id) } : {}) }
}

// authenticate asks GoTrue, on loopback, to verify the bearer token.
export function goTrueAuthenticator(port = gotrue.port) {
  return token => new Promise((resolve, reject) => {
    const req = http.get({ host: gotrue.host, port, path: '/user', headers: { Authorization: `Bearer ${token}` }, timeout: 5000 }, res => {
      let body = ''
      res.setEncoding('utf8')
      res.on('data', chunk => { if (body.length < 65536) body += chunk })
      res.on('end', () => {
        if (res.statusCode !== 200) return reject(new HTTPError(res.statusCode >= 500 ? 503 : 401, res.statusCode >= 500 ? 'auth_unavailable' : 'not_authenticated'))
        try { resolve(claimsFromToken(token, JSON.parse(body))) } catch (err) { reject(err instanceof HTTPError ? err : new HTTPError(401, 'not_authenticated')) }
      })
    })
    req.on('timeout', () => req.destroy())
    req.on('error', () => reject(new HTTPError(503, 'auth_unavailable')))
  })
}

// organizationClaims are the claims the custom access token hook adds to a
// first-party token with AUTH_ORG_CLAIMS on: the session's active
// organization, read with the current membership (sql/0015), or none. An
// organization that requires a second factor (sql/0016) is left out below
// aal2. The token's own aal claim decides: GoTrue calls the hook inside the
// transaction that raises the session, before the row shows it.
export async function organizationClaims(pool, { sub, session_id: session, aal }) {
  if (!uuid.test(sub ?? '') || !uuid.test(session ?? '')) return {}
  const { rows: [org] } = await pool.query(`SELECT o.id, o.slug, o.mfa_required, m.role, r.permissions FROM gregale_auth.session_organizations s
      JOIN gregale_auth.organizations o ON o.id = s.org_id
      JOIN gregale_auth.memberships m ON m.org_id = s.org_id AND m.user_id = $2
      JOIN gregale_auth.roles r ON r.name = m.role
    WHERE s.session_id = $1`, [session, sub])
  if (!org || (org.mfa_required && aal !== 'aal2')) return {}
  return { org_id: org.id, org_slug: org.slug, org_role: org.role, org_permissions: org.permissions }
}

// sessionLive leaves out sessions past the app's limits: GoTrue refuses to
// refresh them but keeps their rows until they are signed out.
export function sessionLive(session, policy, now = Date.now()) {
  const minutes = value => value * 60 * 1000
  if (policy?.maxAgeMinutes && new Date(session.created_at).getTime() + minutes(policy.maxAgeMinutes) < now) return false
  if (policy?.idleMinutes && new Date(session.last_active_at).getTime() + minutes(policy.idleMinutes) < now) return false
  return true
}

// liveSessions is the caller's session list without the sessions GoTrue
// would refuse to refresh. With one session per user only the most recently
// active one lives (GoTrue revokes the others at their next refresh); the
// caller's own session always shows, since its access token still works.
export function liveSessions(sessions, policy, now = Date.now()) {
  const live = sessions.filter(session => session.current || sessionLive(session, policy, now))
  if (!policy?.singlePerUser) return live
  const active = session => new Date(session.last_active_at).getTime()
  const newest = live.filter(session => sessionLive(session, policy, now)).reduce((best, session) => !best || active(session) > active(best) ? session : best, null)
  return live.filter(session => session.current || session === newest)
}

export async function inSession(pool, claims, work) {
  const client = await pool.connect()
  try {
    await client.query('BEGIN')
    await client.query(`SELECT set_config('request.jwt.claims', $1, true)`, [JSON.stringify(claims)])
    const result = await work(client)
    await client.query('COMMIT')
    return result
  } catch (err) {
    await client.query('ROLLBACK').catch(() => {})
    throw err
  } finally {
    client.release()
  }
}

function sqlError(err) {
  if (err instanceof HTTPError) return err
  if (err instanceof ProductEmailError) return new HTTPError(err.status, err.code)
  if (err instanceof TeamError) return new HTTPError(err.status, err.code)
  if (err instanceof EmailError || err instanceof WorkflowError) return new HTTPError(err.status, err.code)
  const status = codes.get(err?.code)
  if (status) return new HTTPError(status, stableMessages.has(err.message) ? err.message : 'invalid_request')
  return new HTTPError(500, 'internal_error')
}

function requireUUID(value) {
  if (!uuid.test(value)) throw new HTTPError(400, 'invalid_id')
  return value
}

function requireString(body, name, max) {
  const value = body?.[name]
  if (typeof value !== 'string' || value.length === 0 || value.length > max) throw new HTTPError(400, `invalid_${name}`)
  return value
}

// An audit log cursor is the last entry's position at the database's full
// precision with its ID, so a page never skips entries made in the same
// millisecond. auditPage reads ?cursor=&limit=<1..100>; auditReply adds the
// next page's cursor.
export const auditColumns = 'id, occurred_at, occurred_at::text AS position, action, actor_id, actor_email, target_user_id, target_email, data'

export function auditPage(query) {
  const cursor = query?.get('cursor') ?? null
  let at = null, id = null
  if (cursor !== null) {
    try { [at, id] = JSON.parse(Buffer.from(cursor, 'base64url').toString('utf8')) } catch { throw new HTTPError(400, 'invalid_cursor') }
    // PostgreSQL's own text for a timestamptz, which it reads back exactly.
    if (typeof at !== 'string' || !/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}(\.\d{1,6})?[+-]\d{2}(:\d{2}){0,2}$/.test(at) || !uuid.test(id ?? '')) throw new HTTPError(400, 'invalid_cursor')
  }
  const limit = Number(query?.get('limit') ?? 50)
  if (!Number.isInteger(limit) || limit < 1 || limit > limits.adminPageSize) throw new HTTPError(400, 'invalid_limit')
  return { at, id, limit }
}

export function auditReply(rows, limit) {
  const entries = rows.map(({ position, ...entry }) => entry)
  const last = rows.at(-1)
  return [200, { entries, next: rows.length === limit ? Buffer.from(JSON.stringify([last.position, last.id])).toString('base64url') : null }]
}

// routeOrganization serves /orgs/<id>/..., or returns null for a path it
// does not know.
async function routeOrganization({ run, rows, services, invitations }, org, method, parts, body) {
  if (parts.length === 2 && method === 'PATCH') {
    if (typeof body?.mfa_required !== 'boolean') throw new HTTPError(400, 'invalid_mfa_required')
    const [updated] = await rows('SELECT id, slug, name, mfa_required FROM gregale_auth.set_organization_mfa($1, $2)', [org, body.mfa_required])
    return [200, updated]
  }
  if (parts[2] === 'email-recipient') {
    if (services.emailEnabled !== true) throw new HTTPError(404, 'not_found')
    return emailConsent(run, method, parts, body)
  }
  if (parts[2] === 'team' || parts[2] === 'team-actions') {
    if (services.teamEnabled !== true) throw new HTTPError(404, 'not_found')
    return routeTeam({ run, method, parts, body, invitations, query: services.query })
  }
  if (parts[2] === 'domains' || parts[2] === 'sso') {
    // Domains come with self-service SSO, or alone with AUTH_ORG_DOMAINS.
    if (!services.orgSSO && !(parts[2] === 'domains' && services.orgDomains === true)) throw new HTTPError(403, 'org_sso_disabled')
    return orgSSORoute({ rows, ...services.orgSSO, orgDomains: services.orgDomains === true }, org, method, parts.slice(2), body)
  }
  // The organization's audit log (sql/0020), newest first; next is the
  // cursor for the following page.
  if (parts[2] === 'audit-log' && parts.length === 3 && method === 'GET') {
    const { at, id, limit } = auditPage(services.query)
    return auditReply(await rows(`SELECT ${auditColumns} FROM gregale_auth.org_audit_entries($1, $2, $3, $4)`, [org, at, id, limit]), limit)
  }
  if (parts[2] === 'members' && parts.length === 3 && method === 'GET') {
    return [200, { members: await rows('SELECT user_id, email, role, created_at, mfa_enabled FROM gregale_auth.list_members($1)', [org]) }]
  }
  if (parts[2] === 'members' && parts.length === 4) {
    const member = requireUUID(parts[3])
    if (method === 'PATCH') {
      const [updated] = await rows('SELECT org_id, user_id, role FROM gregale_auth.set_member_role($1, $2, $3)', [org, member, requireString(body, 'role', 32)])
      return [200, updated]
    }
    if (method === 'DELETE') { await rows('SELECT gregale_auth.remove_member($1, $2)', [org, member]); return [204, null] }
  }
  if (parts[2] === 'invitations' && parts.length === 3) {
    if (method === 'GET') return [200, { invitations: await rows('SELECT id, email, role, expires_at, created_at FROM gregale_auth.list_invitations($1)', [org]) }]
    if (method === 'POST') {
      const ttl = body?.expires_in ?? limits.invitationSeconds
      if (!Number.isInteger(ttl)) throw new HTTPError(400, 'invalid_expires_in')
      const { token, hash } = newInvitationToken()
      const args = [org, requireString(body, 'email', 320), requireString(body, 'role', 32), hash, ttl]
      // The token is stored only as a hash. With an email provider it goes
      // only to the invited address, sent before the invitation commits so
      // a failed send leaves nothing behind; otherwise the inviter gets it once.
      const created = await run(async c => {
        const { rows: [invitation] } = await c.query('SELECT i.id, i.email, i.role, i.expires_at, o.name AS org_name FROM gregale_auth.invite($1, $2, $3, $4, $5) i JOIN gregale_auth.organizations o ON o.id = i.org_id', args)
        const { org_name: orgName, ...fields } = invitation
        if (invitations.mailer) await invitations.mailer.send(invitationMessage(fields.email, { external: invitations.external, app: invitations.app, orgName, token, templates: await loadEmailTemplates(c) }), `invitation-${fields.id}`)
        return fields
      })
      return [201, invitations.mailer ? { ...created, delivered: true } : { ...created, delivered: false, token }]
    }
  }
  if (parts[2] === 'invitations' && parts.length === 4 && method === 'DELETE') {
    await rows('SELECT gregale_auth.revoke_invitation($1, $2)', [org, requireUUID(parts[3])])
    return [204, null]
  }
  return null
}

// routes returns [status, body] for one authenticated request.
export async function route(pool, claims, method, path, body, services = {}) {
  const invitations = services.invitations ?? {}
  const parts = path.split('/').filter(Boolean)
  // A third-party OAuth client may only check its user's permissions.
  if (claims.client_id && !(path === '/authorize' && method === 'POST')) throw new HTTPError(403, 'oauth_client_token')
  const run = work => inSession(pool, claims, work)
  const rows = async (sql, args) => (await run(c => c.query(sql, args))).rows
  if (parts.length === 1 && parts[0] === 'orgs') {
    if (method === 'GET') return [200, { organizations: await rows('SELECT id, slug, name, role, created_at, mfa_required FROM gregale_auth.list_organizations()') }]
    if (method === 'POST') {
      const [org] = await rows('SELECT id, slug, name, created_at FROM gregale_auth.create_organization($1, $2)', [requireString(body, 'slug', 63), requireString(body, 'name', 200)])
      return [201, org]
    }
  }
  if (parts[0] === 'orgs' && parts.length >= 2) {
    const org = requireUUID(parts[1])
    try {
      const routed = await routeOrganization({ run, rows, services, invitations }, org, method, parts, body)
      if (routed) return routed
    } catch (err) {
      // A member refused because the organization requires a second factor
      // this session has not verified (sql/0016) is told so.
      if (sqlError(err).code === 'forbidden' && (await rows('SELECT gregale_auth.mfa_blocks($1) AS blocked', [org]))[0].blocked) throw new HTTPError(403, 'mfa_required')
      throw err
    }
  }
  // Joining an organization by the caller's email domain (sql/0017).
  if (services.orgDomains === true) {
    const joined = await domainJoinRoute(rows, method, path)
    if (joined) return joined
  }
  // The caller's own sessions (sql/0013); the current one ends through
  // GoTrue's logout, and all the others through logout?scope=others.
  if (path === '/sessions' && method === 'GET') {
    // A session support staff opened as the caller is marked (impersonation.mjs).
    const listed = await rows(`SELECT s.id, s.created_at, s.last_active_at, s.user_agent, s.ip, s.aal, s.current,
        EXISTS (SELECT 1 FROM gregale_auth.impersonations i WHERE i.session_id = s.id AND i.ended_at IS NULL AND i.expires_at > now()) AS impersonated
      FROM gregale_auth.list_sessions() s`)
    return [200, { sessions: liveSessions(listed, services.sessions) }]
  }
  if (parts[0] === 'sessions' && parts.length === 2 && method === 'DELETE') {
    await rows('SELECT gregale_auth.end_session($1)', [requireUUID(parts[1])])
    return [204, null]
  }
  // Deleting one's own account, when the app allows it (AUTH_SELF_DELETION).
  if (path === '/account' && method === 'DELETE') {
    if (!services.deleteAccount) throw new HTTPError(404, 'not_found')
    return services.deleteAccount(claims)
  }
  // The organization this session acts in, carried in its next tokens
  // (AUTH_ORG_CLAIMS); null clears it. The client refreshes afterwards.
  if (path === '/active-organization' && method === 'POST') {
    if (services.orgClaims !== true) throw new HTTPError(404, 'not_found')
    const org = body?.org === null ? null : requireUUID(typeof body?.org === 'string' ? body.org : '')
    const [active] = await rows('SELECT id, slug, role FROM gregale_auth.set_active_organization($1)', [org])
    return [200, { organization: active ?? null }]
  }
  if (path === '/invitations/accept' && method === 'POST') {
    const [membership] = await rows('SELECT a.org_id, a.role, o.mfa_required FROM gregale_auth.accept_invitation($1) a JOIN gregale_auth.organizations o ON o.id = a.org_id', [hashToken(body?.token)])
    return [200, membership]
  }
  if (path === '/storage/sign' && method === 'POST') {
    const bucket = requireString(body, 'bucket', 64)
    const binding = services.storage?.get(bucket)
    if (!binding) throw new HTTPError(404, 'unknown_bucket')
    const operation = body.operation ?? 'read'
    if (operation !== 'read' && operation !== 'write') throw new HTTPError(400, 'invalid_operation')
    const resource = objectResource(bucket, body.key)
    if (!resource) throw new HTTPError(400, 'invalid_key')
    const [{ allowed }] = await rows('SELECT gregale_auth.authorize($1, $2) AS allowed', [`storage:${operation}`, resource])
    if (!allowed) throw new HTTPError(403, 'forbidden')
    const httpMethod = operation === 'write' ? 'PUT' : 'GET'
    const url = presign({ method: httpMethod, url: objectURL(binding, body.key), region: binding.region, accessKeyId: binding.accessKeyId, secretAccessKey: binding.secretAccessKey, expires: limits.storageURLSeconds })
    return [200, { url, method: httpMethod, expires_in: limits.storageURLSeconds }]
  }
  const start = /^\/workflows\/([a-z0-9][a-z0-9_-]{0,62})\/start$/.exec(path)
  if (start && method === 'POST') {
    const org = requireUUID(body?.org ?? '')
    const idempotencyKey = body?.idempotency_key ?? randomBytes(16).toString('hex')
    if (typeof idempotencyKey !== 'string' || !/^[A-Za-z0-9_-]{1,64}$/.test(idempotencyKey)) throw new HTTPError(400, 'invalid_idempotency_key')
    const [{ allowed }] = await rows('SELECT gregale_auth.authorize($1, $2) AS allowed', ['workflow:start', `org/${org}/workflow/${start[1]}`])
    if (!allowed) throw new HTTPError(403, 'forbidden')
    if (!services.workflows) throw new HTTPError(503, 'workflows_unconfigured')
    return [202, await services.workflows({ name: start[1], org, user: claims.sub, input: body.input, idempotencyKey })]
  }
  if (path === '/authorize' && method === 'POST') {
    const [{ allowed }] = await rows('SELECT gregale_auth.authorize($1, $2) AS allowed', [requireString(body, 'action', 64), requireString(body, 'resource', 1024)])
    return [200, { allowed }]
  }
  throw new HTTPError(404, 'not_found')
}

export function readJSON(req, maxBytes) {
  return new Promise((resolve, reject) => {
    if (!['POST', 'PATCH'].includes(req.method)) { req.resume(); return resolve(undefined) }
    if (!/^application\/json(;|$)/i.test(req.headers['content-type'] ?? '')) return reject(new HTTPError(415, 'json_required'))
    let size = 0
    const chunks = []
    req.on('data', chunk => {
      size += chunk.length
      if (size > maxBytes) { reject(new HTTPError(413, 'request_too_large')); req.destroy() } else chunks.push(chunk)
    })
    req.on('end', () => { try { resolve(JSON.parse(Buffer.concat(chunks).toString('utf8') || '{}')) } catch { reject(new HTTPError(400, 'invalid_json')) } })
    req.on('error', reject)
  })
}

export function createAuthzHandler({ pool, authenticate, origins = [], invitations = {}, storage, workflows, orgSSO, orgDomains = false, teamEnabled = false, emailEnabled = false, deleteAccount = null, orgClaims = false, sessions = null }) {
  const allowed = new Set(origins)
  return async (req, res, path) => {
    const origin = req.headers.origin
    const headers = { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' }
    if (origin && allowed.has(origin)) Object.assign(headers, { 'Access-Control-Allow-Origin': origin, Vary: 'Origin' })
    if (emailEnabled !== true && emailPath(path)) {
      res.writeHead(404, headers); return res.end('{"status":404,"code":"not_found"}')
    }
    if (teamEnabled !== true && /^\/orgs\/[^/]+\/(?:team|team-actions)(?:\/|$)/.test(path)) {
      res.writeHead(404, headers); return res.end('{"status":404,"code":"not_found"}')
    }
    if (req.method === 'OPTIONS') {
      if (!origin || !allowed.has(origin)) { res.writeHead(403, headers); return res.end('{"code":"origin_not_allowed"}') }
      res.writeHead(204, { ...headers, 'Access-Control-Allow-Methods': 'GET,POST,PATCH,DELETE,OPTIONS', 'Access-Control-Allow-Headers': 'Authorization,Content-Type', 'Access-Control-Max-Age': '600' })
      return res.end()
    }
    try {
      if (emailEnabled && emailPath(path) && path.split('/').filter(Boolean).length === 4) {
        const [status, payload] = await resolveEmailRecipient(pool, req.method, path, req.headers.authorization)
        res.writeHead(status, headers); return res.end(JSON.stringify(payload))
      }
      const token = req.headers.authorization?.match(/^Bearer ([A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+)$/)?.[1]
      if (!token) throw new HTTPError(401, 'not_authenticated')
      const body = await readJSON(req, limits.authzBodyBytes)
      const claims = await authenticate(token)
      const [status, payload] = await route(pool, claims, req.method, path, body, { invitations, storage, workflows, orgSSO, orgDomains, teamEnabled, emailEnabled, deleteAccount, orgClaims, sessions, query: new URL(req.url, 'http://auth.local').searchParams })
      res.writeHead(status, headers)
      res.end(payload === null ? undefined : JSON.stringify(payload))
    } catch (err) {
      const failure = sqlError(err)
      if (failure.status === 500) console.error(JSON.stringify({ level: 'error', event: 'authz_failed', sqlstate: err?.code ?? null }))
      if (res.headersSent) return res.destroy()
      res.writeHead(failure.status, headers)
      res.end(JSON.stringify({ status: failure.status, code: failure.code }))
    }
  }
}
