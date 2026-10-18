import { createHash, randomBytes } from 'node:crypto'
import http from 'node:http'
import { gotrue, limits } from './config.mjs'
import { EmailError, invitationMessage } from './email.mjs'
import { objectResource, objectURL, presign } from './storage.mjs'
import { WorkflowError } from './workflows.mjs'
import { routeTeam, TeamError } from './team.mjs'
import { emailPath, emailConsent, resolveEmailRecipient, ProductEmailError } from './product-email.mjs'
import { orgSSORoute } from './orgsso.mjs'

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
  'domain_taken', 'domain_limit', 'invalid_domain', 'domain_in_use', 'domain_not_verified', 'sso_provider_limit', 'team_limit'])

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

// routes returns [status, body] for one authenticated request.
export async function route(pool, claims, method, path, body, services = {}) {
  const invitations = services.invitations ?? {}
  const parts = path.split('/').filter(Boolean)
  // A third-party OAuth client may only check its user's permissions.
  if (claims.client_id && !(path === '/authorize' && method === 'POST')) throw new HTTPError(403, 'oauth_client_token')
  const run = work => inSession(pool, claims, work)
  const rows = async (sql, args) => (await run(c => c.query(sql, args))).rows
  if (parts.length === 1 && parts[0] === 'orgs') {
    if (method === 'GET') return [200, { organizations: await rows('SELECT id, slug, name, role, created_at FROM gregale_auth.list_organizations()') }]
    if (method === 'POST') {
      const [org] = await rows('SELECT id, slug, name, created_at FROM gregale_auth.create_organization($1, $2)', [requireString(body, 'slug', 63), requireString(body, 'name', 200)])
      return [201, org]
    }
  }
  if (parts[0] === 'orgs' && parts.length >= 3) {
    const org = requireUUID(parts[1])
    if (parts[2] === 'email-recipient') {
      if (services.emailEnabled !== true) throw new HTTPError(404, 'not_found')
      return emailConsent(run, method, parts, body)
    }
    if (parts[2] === 'team' || parts[2] === 'team-actions') {
      if (services.teamEnabled !== true) throw new HTTPError(404, 'not_found')
      return routeTeam({ run, method, parts, body, invitations, query: services.query })
    }
    if (parts[2] === 'domains' || parts[2] === 'sso') {
      if (!services.orgSSO) throw new HTTPError(403, 'org_sso_disabled')
      return orgSSORoute({ rows, ...services.orgSSO }, org, method, parts.slice(2), body)
    }
    if (parts[2] === 'members' && parts.length === 3 && method === 'GET') {
      return [200, { members: await rows('SELECT user_id, email, role, created_at FROM gregale_auth.list_members($1)', [org]) }]
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
          if (invitations.mailer) await invitations.mailer.send(invitationMessage(fields.email, { external: invitations.external, app: invitations.app, orgName, token }), `invitation-${fields.id}`)
          return fields
        })
        return [201, invitations.mailer ? { ...created, delivered: true } : { ...created, delivered: false, token }]
      }
    }
    if (parts[2] === 'invitations' && parts.length === 4 && method === 'DELETE') {
      await rows('SELECT gregale_auth.revoke_invitation($1, $2)', [org, requireUUID(parts[3])])
      return [204, null]
    }
  }
  if (path === '/invitations/accept' && method === 'POST') {
    const [membership] = await rows('SELECT org_id, role FROM gregale_auth.accept_invitation($1)', [hashToken(body?.token)])
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

export function createAuthzHandler({ pool, authenticate, origins = [], invitations = {}, storage, workflows, orgSSO, teamEnabled = false, emailEnabled = false }) {
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
      const [status, payload] = await route(pool, claims, req.method, path, body, { invitations, storage, workflows, orgSSO, teamEnabled, emailEnabled, query: new URL(req.url, 'http://auth.local').searchParams })
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
