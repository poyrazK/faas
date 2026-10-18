import { createHash, randomBytes } from 'node:crypto'
import { limits } from './config.mjs'
import { invitationMessage, loadEmailTemplates } from './email.mjs'

export class TeamError extends Error {
  constructor(status, code) { super(code); this.status = status; this.code = code }
}
const uuid = value => typeof value === 'string' && /^[a-f0-9]{8}-(?:[a-f0-9]{4}-){3}[a-f0-9]{12}$/.test(value) && value !== '00000000-0000-0000-0000-000000000000'
const hex = value => typeof value === 'string' && /^[a-f0-9]{64}$/.test(value)
const role = value => typeof value === 'string' && /^[a-z][a-z0-9_-]{0,31}$/.test(value)
const object = value => value && typeof value === 'object' && !Array.isArray(value)
const keys = (value, allowed) => object(value) && Object.keys(value).every(key => allowed.includes(key))
const canonical = value => Array.isArray(value) ? value.map(canonical) : object(value) ? Object.fromEntries(Object.keys(value).sort().map(key => [key, canonical(value[key])])) : value
const hash = value => createHash('sha256').update(JSON.stringify(canonical(value))).digest('hex')
const fail = code => { throw new TeamError(400, code) }

function normalize(body) {
  if (!keys(body, ['id', 'action', 'target_id', 'email', 'role', 'expires_in', 'expected_snapshot', 'context']) || !uuid(body.id) || !hex(body.expected_snapshot)) fail('invalid_team_action')
  const context = body.context ?? {}
  if (!keys(context, ['product_id', 'tenant_id', 'auth_app_id', 'catalog_revision', 'review_hash']) || Buffer.byteLength(JSON.stringify(context)) > limits.teamContextBytes) fail('invalid_team_context')
  for (const [key, value] of Object.entries(context)) if (!(key.endsWith('_id') ? uuid(value) : hex(value))) fail('invalid_team_context')
  const out = { id: body.id, action: body.action, expected_snapshot: body.expected_snapshot, context }
  switch (body.action) {
    case 'invite': {
      if ('target_id' in body || !role(body.role) || typeof body.email !== 'string') fail('invalid_team_action')
      const email = body.email.trim().toLowerCase(), ttl = body.expires_in ?? limits.invitationSeconds
      if (Buffer.byteLength(email) > limits.teamEmailBytes || !/^[^\x00-\x20\x7f<>@]+@[^\x00-\x20\x7f<>@]+$/.test(email) || !Number.isInteger(ttl) || ttl < limits.teamInvitationMinSeconds || ttl > limits.teamInvitationMaxSeconds) fail('invalid_team_action')
      return { ...out, email, role: body.role, expires_in: ttl }
    }
    case 'set_role':
      if (!uuid(body.target_id) || !role(body.role) || 'email' in body || 'expires_in' in body) fail('invalid_team_action')
      return { ...out, target_id: body.target_id, role: body.role }
    case 'remove_member': case 'revoke_invitation':
      if (!uuid(body.target_id) || 'role' in body || 'email' in body || 'expires_in' in body) fail('invalid_team_action')
      return { ...out, target_id: body.target_id }
    default: fail('invalid_team_action')
  }
}

async function state(client, org) {
  const { rows: [row] } = await client.query('SELECT gregale_auth.team_state($1,$2,$3) AS state', [org, limits.teamMembers, limits.teamInvitations])
  const value = row.state
  return { ...value, snapshot: hash(value), protocol: 1 }
}
const safeReceipt = stored => { const { fingerprint, ...receipt } = stored; return receipt }

async function apply(client, org, request, invitations, before) {
  const target = before.members.find(member => member.user_id === request.target_id)
  if (request.action === 'set_role') {
    const { rows: [updated] } = await client.query('SELECT org_id,user_id,role FROM gregale_auth.set_member_role($1,$2,$3)', [org, request.target_id, request.role])
    return { ...updated, email: target?.email ?? '', before_role: target?.role ?? '', after_role: updated.role }
  }
  if (request.action === 'remove_member') {
    await client.query('SELECT gregale_auth.remove_member($1,$2)', [org, request.target_id])
    return { user_id: request.target_id, email: target?.email ?? '', before_role: target?.role ?? '', after_role: '' }
  }
  if (request.action === 'revoke_invitation') {
    const pending = before.invitations.find(invite => invite.id === request.target_id)
    if (!pending) throw new TeamError(404, 'not_found')
    await client.query('SELECT gregale_auth.revoke_invitation($1,$2)', [org, request.target_id])
    return { invitation_id: request.target_id, email: pending.email, before_role: pending.role, after_role: '' }
  }
  if (before.invitations.some(invite => invite.email.toLowerCase() === request.email) || before.members.some(member => String(member.email ?? '').toLowerCase() === request.email)) throw new TeamError(409, 'already_invited_or_member')
  const token = randomBytes(32).toString('base64url'), digest = createHash('sha256').update(Buffer.from(token, 'base64url')).digest()
  const { rows: [created] } = await client.query('SELECT i.id,i.email,i.role,i.expires_at,o.name AS org_name FROM gregale_auth.invite($1,$2,$3,$4,$5) i JOIN gregale_auth.organizations o ON o.id=i.org_id', [org, request.email, request.role, digest, request.expires_in])
  const { org_name: orgName, ...fields } = created
  if (invitations.mailer) await invitations.mailer.send(invitationMessage(fields.email, { external: invitations.external, app: invitations.app, orgName, token, templates: await loadEmailTemplates(client) }), `invitation-${fields.id}`)
  return { ...fields, delivered: Boolean(invitations.mailer), before_role: '', after_role: fields.role, ...(invitations.mailer ? {} : { token }) }
}

async function confirm(run, org, body, invitations) {
  const request = normalize(body)
  return run(async client => {
    const before = await state(client, org)
    const { rows: [found] } = await client.query('SELECT gregale_auth.team_read_action($1,$2) AS action', [org, request.id])
    const fingerprint = hash({ actor_id: before.actor_id, org_id: org, request })
    if (found.action) {
      if (found.action.fingerprint !== fingerprint) throw new TeamError(409, 'team_action_conflict')
      return [200, { receipt: safeReceipt(found.action), replayed: true }]
    }
    if (before.snapshot !== request.expected_snapshot) throw new TeamError(409, 'team_review_changed')
    const result = await apply(client, org, request, invitations, before)
    // A manual invitation token is returned once, never retained in its audit.
    const { token, ...evidence } = result
    const { rows: [stored] } = await client.query('SELECT gregale_auth.team_record_action($1,$2,$3,$4,$5,$6::jsonb,$7::jsonb) AS action', [org, request.id, before.actor_role, request.action, fingerprint, JSON.stringify(request), JSON.stringify(evidence)])
    return [200, { receipt: safeReceipt(stored.action), replayed: false, ...(token ? { token } : {}) }]
  })
}

function cursor(raw, org) {
  if (!raw) return [null, null]
  try {
    if (raw.length > limits.teamCursorBytes || !/^[A-Za-z0-9_-]+$/.test(raw)) throw new Error()
    const value = JSON.parse(Buffer.from(raw, 'base64url').toString('utf8'))
    if (!keys(value, ['org', 'at', 'id']) || value.org !== org || !uuid(value.id) || typeof value.at !== 'string' || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/.test(value.at) || !Number.isFinite(Date.parse(value.at)) || Buffer.from(JSON.stringify(value)).toString('base64url') !== raw) throw new Error()
    return [value.at, value.id]
  } catch { throw new TeamError(400, 'invalid_team_cursor') }
}

export async function routeTeam({ run, method, parts, body, invitations = {}, query = new URLSearchParams() }) {
  const org = parts[1]
  if (!uuid(org)) fail('invalid_id')
  if (parts[2] === 'team' && parts.length === 3 && method === 'GET') return [200, await run(client => state(client, org))]
  if (parts[2] !== 'team-actions') throw new TeamError(404, 'not_found')
  if (parts.length === 3 && method === 'POST') return confirm(run, org, body, invitations)
  if (parts.length === 4 && method === 'GET') {
    if (!uuid(parts[3])) fail('invalid_id')
    return run(async client => {
      const { rows: [row] } = await client.query('SELECT gregale_auth.team_read_action($1,$2) AS action', [org, parts[3]])
      if (!row.action) throw new TeamError(404, 'not_found')
      return [200, safeReceipt(row.action)]
    })
  }
  if (parts.length === 3 && method === 'GET') {
    if ([...query.keys()].some(key => key !== 'cursor') || query.getAll('cursor').length > 1) fail('invalid_team_cursor')
    const [at, id] = cursor(query.get('cursor') ?? '', org)
    return run(async client => {
      const { rows } = await client.query('SELECT gregale_auth.team_list_actions($1,$2,$3,$4) AS action', [org, at, id, limits.teamHistoryPageSize + 1])
      const actions = rows.slice(0, limits.teamHistoryPageSize).map(row => row.action)
      const last = actions.at(-1)
      return [200, { actions, ...(rows.length > actions.length ? { next_cursor: Buffer.from(JSON.stringify({ org, at: last.created_at, id: last.id })).toString('base64url') } : {}) }]
    })
  }
  throw new TeamError(404, 'not_found')
}
