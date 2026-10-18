import { createHash, randomBytes } from 'node:crypto'
import { limits } from './config.mjs'

// /scim/v2: SCIM 2.0 provisioning for organization SSO (ADR-1190, sql/0006).
// A customer's identity provider provisions and deprovisions its people with
// a bearer token scoped to one SSO provider. GoTrue v2.197.0 ships only an
// experimental SCIM stub, so the front serves the Users resource itself:
// create, list with a userName or externalId filter, get, replace, patch
// (Okta and Entra ID forms) and delete. Groups are not supported; the SSO
// provider's organization link decides membership.

const schema = {
  user: 'urn:ietf:params:scim:schemas:core:2.0:User',
  list: 'urn:ietf:params:scim:api:messages:2.0:ListResponse',
  error: 'urn:ietf:params:scim:api:messages:2.0:Error',
  patch: 'urn:ietf:params:scim:api:messages:2.0:PatchOp',
  config: 'urn:ietf:params:scim:schemas:core:2.0:ServiceProviderConfig',
}
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
// A deprovisioned person stays banned until the identity provider
// reactivates them; GoTrue takes the ban as a Go duration.
const deprovisionBan = '876000h'

export class ScimError extends Error {
  constructor(status, detail, scimType) { super(detail); this.status = status; this.scimType = scimType }
}

export function hashScimToken(token) {
  return createHash('sha256').update(token).digest()
}

export function newScimToken() {
  const token = `gscim_${randomBytes(32).toString('base64url')}`
  return { token, hash: hashScimToken(token) }
}

const text = (value, max) => typeof value === 'string' && value.trim() !== '' && value.length <= max ? value.trim() : undefined

// userInput reads the attributes Gregale keeps from a SCIM User. userName is
// required; the email is the primary address, or the first one.
export function userInput(body) {
  if (!body || typeof body !== 'object') throw new ScimError(400, 'A SCIM User body is required', 'invalidSyntax')
  const userName = text(body.userName, 320)
  if (!userName) throw new ScimError(400, 'userName is required', 'invalidValue')
  const emails = Array.isArray(body.emails) ? body.emails.filter(e => e && typeof e.value === 'string') : []
  const email = text((emails.find(e => e.primary === true) ?? emails[0])?.value, 320)
  return {
    user_name: userName, external_id: text(body.externalId, 256) ?? null, email: email ?? null,
    display_name: text(body.displayName, 256) ?? null, given_name: text(body.name?.givenName, 128) ?? null,
    family_name: text(body.name?.familyName, 128) ?? null, active: activeValue(body.active ?? true),
  }
}

// Entra ID sends booleans as strings ("False") in PATCH values.
function activeValue(value) {
  if (typeof value === 'boolean') return value
  if (typeof value === 'string' && /^(true|false)$/i.test(value)) return value.toLowerCase() === 'true'
  throw new ScimError(400, 'active must be a boolean', 'invalidValue')
}

// applyPatch applies RFC 7644 PatchOp operations to a stored record. It
// accepts the forms identity providers send: Okta's {op: replace, value:
// {active: false}} and Entra ID's {op: Replace, path: active, value: "False"}.
export function applyPatch(row, body) {
  if (!body || !Array.isArray(body.Operations) || body.Operations.length === 0 || body.Operations.length > 100) throw new ScimError(400, 'Operations are required', 'invalidSyntax')
  const next = { ...row }
  const set = (path, value) => {
    switch (path.toLowerCase()) {
      case 'active': next.active = activeValue(value); break
      case 'username': next.user_name = text(value, 320) ?? next.user_name; break
      case 'externalid': next.external_id = text(value, 256) ?? null; break
      case 'displayname': next.display_name = text(value, 256) ?? null; break
      case 'name.givenname': next.given_name = text(value, 128) ?? null; break
      case 'name.familyname': next.family_name = text(value, 128) ?? null; break
      case 'name': next.given_name = text(value?.givenName, 128) ?? next.given_name; next.family_name = text(value?.familyName, 128) ?? next.family_name; break
      case 'emails': case 'emails[type eq "work"].value': {
        const list = Array.isArray(value) ? value : [{ value }]
        next.email = text((list.find(e => e?.primary === true) ?? list[0])?.value ?? list[0], 320) ?? next.email
        break
      }
      default: break // Attributes Gregale does not keep are ignored, as RFC 7644 allows.
    }
  }
  for (const operation of body.Operations) {
    const op = String(operation?.op ?? '').toLowerCase()
    if (op !== 'replace' && op !== 'add') throw new ScimError(400, `Unsupported op ${operation?.op}`, 'invalidSyntax')
    if (typeof operation.path === 'string') set(operation.path, operation.value)
    else if (operation.value && typeof operation.value === 'object') for (const [path, value] of Object.entries(operation.value)) set(path, value)
  }
  return next
}

// parseFilter supports what identity providers send to find a person:
// userName eq "..." and externalId eq "...".
export function parseFilter(filter) {
  if (filter === null || filter === undefined || filter === '') return null
  const match = /^\s*(userName|externalId)\s+eq\s+"((?:[^"\\]|\\.)*)"\s*$/i.exec(filter)
  if (!match) throw new ScimError(400, 'Only userName eq and externalId eq filters are supported', 'invalidFilter')
  return { attribute: match[1].toLowerCase() === 'username' ? 'user_name' : 'external_id', value: match[2].replace(/\\(.)/g, '$1') }
}

export function userResource(row, base) {
  return {
    schemas: [schema.user], id: row.id, ...(row.external_id ? { externalId: row.external_id } : {}), userName: row.user_name,
    name: { ...(row.given_name ? { givenName: row.given_name } : {}), ...(row.family_name ? { familyName: row.family_name } : {}) },
    ...(row.display_name ? { displayName: row.display_name } : {}),
    emails: row.email ? [{ value: row.email, primary: true }] : [], active: row.active,
    meta: { resourceType: 'User', created: new Date(row.created_at).toISOString(), lastModified: new Date(row.updated_at).toISOString(), location: `${base}/scim/v2/Users/${row.id}` },
  }
}

const columns = 'id, provider_id, external_id, user_name, email, display_name, given_name, family_name, active, user_id, created_at, updated_at'

// A linked person's access follows the identity provider: deprovisioning
// bans them, ends their sessions and removes them from the provider's
// organization (never its last owner); reactivating lifts the ban and
// returns them to it.
async function applyAccess({ pool, admin }, row) {
  if (!row.user_id) return
  const response = await admin('PUT', `/admin/users/${row.user_id}`, { ban_duration: row.active ? 'none' : deprovisionBan })
  if (response.status === 404) return
  if (response.status !== 200) throw new ScimError(503, 'The Auth server is unavailable')
  if (row.active) {
    await pool.query(`INSERT INTO gregale_auth.memberships (org_id, user_id, role) SELECT s.org_id, $2, s.role FROM gregale_auth.sso_organizations s
      WHERE s.provider_id = $1 ON CONFLICT (org_id, user_id) DO NOTHING`, [row.provider_id, row.user_id])
    return
  }
  await pool.query('DELETE FROM auth.sessions WHERE user_id = $1', [row.user_id])
  await pool.query(`DELETE FROM gregale_auth.memberships m USING gregale_auth.sso_organizations s
    WHERE s.provider_id = $1 AND m.org_id = s.org_id AND m.user_id = $2
      AND NOT (m.role = 'owner' AND (SELECT count(*) FROM gregale_auth.memberships x WHERE x.org_id = m.org_id AND x.role = 'owner') = 1)`, [row.provider_id, row.user_id])
}

// existingUser finds a person who signed in through this provider before
// they were provisioned, so the new record links to them at once.
async function existingUser(pool, provider, email) {
  const { rows } = await pool.query(`SELECT u.id FROM auth.users u JOIN auth.identities i ON i.user_id = u.id
    WHERE i.provider = 'sso:' || $1::text AND lower(u.email) = lower($2)
      AND NOT EXISTS (SELECT 1 FROM gregale_auth.scim_users s WHERE s.user_id = u.id) LIMIT 1`, [provider, email])
  return rows[0]?.id ?? null
}

function unique(err) {
  if (err?.code === '23505') return new ScimError(409, 'A user with this userName or externalId already exists', 'uniqueness')
  if (err?.code === '23514') return new ScimError(400, 'An attribute is too long', 'invalidValue')
  return err
}

const staticResources = base => ({
  '/ServiceProviderConfig': {
    schemas: [schema.config], patch: { supported: true }, bulk: { supported: false, maxOperations: 0, maxPayloadSize: 0 },
    filter: { supported: true, maxResults: limits.scimPageSize }, changePassword: { supported: false }, sort: { supported: false }, etag: { supported: false },
    authenticationSchemes: [{ type: 'oauthbearertoken', name: 'Bearer token', description: 'A token from gregale auth sso scim-token' }],
  },
  '/ResourceTypes': { schemas: [schema.list], totalResults: 1, Resources: [{ schemas: ['urn:ietf:params:scim:schemas:core:2.0:ResourceType'], id: 'User', name: 'User', endpoint: '/Users', schema: schema.user, meta: { location: `${base}/scim/v2/ResourceTypes/User` } }] },
  '/Schemas': { schemas: [schema.list], totalResults: 1, Resources: [{ id: schema.user, name: 'User' }] },
})

export async function scimRoute({ pool, admin, base }, provider, method, url, body) {
  const path = url.pathname.replace(/^\/scim\/v2/, '')
  const fixed = staticResources(base)[path]
  if (fixed && method === 'GET') return [200, fixed]
  const id = /^\/Users\/([^/]+)$/.exec(path)?.[1]
  if (path === '/Users' && method === 'GET') {
    const filter = parseFilter(url.searchParams.get('filter'))
    const start = Math.max(1, Number.parseInt(url.searchParams.get('startIndex') ?? '1', 10) || 1)
    const count = Math.min(limits.scimPageSize, Math.max(0, Number.parseInt(url.searchParams.get('count') ?? String(limits.scimPageSize), 10) || 0))
    // $2 is the filter value or NULL for no filter; $3 says which attribute.
    const args = [provider, filter?.value ?? null, filter?.attribute ?? null]
    const matches = `provider_id = $1 AND ($3::text IS NULL OR ($3 = 'user_name' AND lower(user_name) = lower($2)) OR ($3 = 'external_id' AND external_id = $2))`
    const { rows } = await pool.query(`SELECT ${columns} FROM gregale_auth.scim_users WHERE ${matches} ORDER BY created_at, id LIMIT $4 OFFSET $5`, [...args, count, start - 1])
    const { rows: [{ total }] } = await pool.query(`SELECT count(*)::int AS total FROM gregale_auth.scim_users WHERE ${matches}`, args)
    return [200, { schemas: [schema.list], totalResults: total, startIndex: start, itemsPerPage: rows.length, Resources: rows.map(row => userResource(row, base)) }]
  }
  if (path === '/Users' && method === 'POST') {
    const input = userInput(body)
    const linked = await existingUser(pool, provider, input.email ?? input.user_name)
    let row
    try {
      ;({ rows: [row] } = await pool.query(`INSERT INTO gregale_auth.scim_users (provider_id, external_id, user_name, email, display_name, given_name, family_name, active, user_id)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING ${columns}`,
      [provider, input.external_id, input.user_name, input.email, input.display_name, input.given_name, input.family_name, input.active, linked]))
    } catch (err) { throw unique(err) }
    await applyAccess({ pool, admin }, row)
    return [201, userResource(row, base)]
  }
  if (!id) throw new ScimError(404, 'Not found')
  if (!uuid.test(id)) throw new ScimError(404, 'User not found')
  const { rows: [current] } = await pool.query(`SELECT ${columns} FROM gregale_auth.scim_users WHERE provider_id = $1 AND id = $2`, [provider, id])
  if (!current) throw new ScimError(404, 'User not found')
  if (method === 'GET') return [200, userResource(current, base)]
  if (method === 'DELETE') {
    await applyAccess({ pool, admin }, { ...current, active: false })
    await pool.query('DELETE FROM gregale_auth.scim_users WHERE id = $1', [id])
    return [204, undefined]
  }
  if (method !== 'PUT' && method !== 'PATCH') throw new ScimError(405, 'Method not allowed')
  const next = method === 'PUT' ? { ...current, ...userInput(body) } : applyPatch(current, body)
  let row
  try {
    ;({ rows: [row] } = await pool.query(`UPDATE gregale_auth.scim_users SET external_id = $2, user_name = $3, email = $4, display_name = $5, given_name = $6,
      family_name = $7, active = $8, updated_at = now() WHERE id = $1 RETURNING ${columns}`,
    [id, next.external_id, next.user_name, next.email, next.display_name, next.given_name, next.family_name, next.active]))
  } catch (err) { throw unique(err) }
  if (row.active !== current.active) await applyAccess({ pool, admin }, row)
  return [200, userResource(row, base)]
}

function readBody(req) {
  return new Promise((resolve, reject) => {
    if (!['POST', 'PUT', 'PATCH'].includes(req.method)) { req.resume(); return resolve(undefined) }
    const chunks = []
    let size = 0
    req.on('data', chunk => {
      size += chunk.length
      if (size > limits.scimBodyBytes) { reject(new ScimError(413, 'Request too large')); req.destroy() } else chunks.push(chunk)
    })
    req.on('end', () => { try { resolve(JSON.parse(Buffer.concat(chunks).toString('utf8') || '{}')) } catch { reject(new ScimError(400, 'Invalid JSON', 'invalidSyntax')) } })
    req.on('error', reject)
  })
}

// createScimHandler serves /scim/v2 for the provider whose token the
// request bears; without a valid token it answers 401.
export function createScimHandler({ pool, admin, base }) {
  return async (req, res) => {
    const reply = (status, payload) => {
      res.writeHead(status, { 'Content-Type': 'application/scim+json', 'Cache-Control': 'no-store' })
      res.end(payload === undefined ? undefined : JSON.stringify(payload))
    }
    try {
      const token = /^Bearer (gscim_[A-Za-z0-9_-]{43})$/.exec(req.headers.authorization ?? '')?.[1]
      const { rows: [match] } = token ? await pool.query('SELECT provider_id FROM gregale_auth.scim_tokens WHERE token_hash = $1', [hashScimToken(token)]) : { rows: [] }
      if (!match) { req.resume(); throw new ScimError(401, 'A valid SCIM bearer token is required') }
      const body = await readBody(req)
      const [status, payload] = await scimRoute({ pool, admin, base }, match.provider_id, req.method, new URL(req.url, 'http://scim.invalid'), body)
      reply(status, payload)
    } catch (err) {
      const failure = err instanceof ScimError ? err : new ScimError(500, 'Internal error')
      if (failure.status >= 500) console.error(JSON.stringify({ level: 'error', event: 'scim_failed', sqlstate: err?.code ?? null }))
      if (res.headersSent) return res.destroy()
      reply(failure.status, { schemas: [schema.error], status: String(failure.status), detail: failure.message, ...(failure.scimType ? { scimType: failure.scimType } : {}) })
    }
  }
}
