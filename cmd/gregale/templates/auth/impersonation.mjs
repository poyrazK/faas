import { limits } from './config.mjs'
import { HTTPError } from './authz.mjs'

// Impersonation (ADR-1190, AUTH_IMPERSONATION): the app's operator, with an
// admin key, opens a session as a user to see what they see, for support.
// GoTrue has no admin "sign in as": the front asks GoTrue for a magic link
// and redeems it at once, so the session is real (row-level security,
// authorize() and realtime treat it like any other), then caps its lifetime
// with not_after and records who opened it and why. The access token hook
// marks its tokens with act = { sub: "admin:<key ID>" } from the next token
// on, and the front refuses account changes from such tokens.

// impersonated is true for an access token the hook marked.
export function impersonated(claims) {
  return typeof claims?.act?.sub === 'string' && claims.act.sub.startsWith('admin:')
}

// impersonationBlocked lists what an impersonated session may not do: change
// the person's sign-in methods, password, email, second factors or
// passkeys; link identities; approve third-party apps; end their other
// sessions or delete their account. It may sign itself out.
export function impersonationBlocked(method, url) {
  // GoTrue's router serves /user/ as /user, so trailing slashes go first.
  const path = url.split('?')[0].replace(/\/+$/, '')
  if (method === 'POST' && path === '/auth/v1/logout') return url !== '/auth/v1/logout?scope=local'
  if (method === 'PUT' && path === '/auth/v1/user') return true
  if (/^\/auth\/v1\/(factors|passkeys|user\/identities|reauthenticate)(\/|$)/.test(path)) return true
  if (/^\/auth\/v1\/oauth\/authorizations\/[^/]+\/consent$/.test(path)) return true
  if (method === 'DELETE' && (path === '/authz/v1/account' || path.startsWith('/authz/v1/sessions/'))) return true
  return false
}

// actClaims returns the act claim for a first-party session that is being
// impersonated, or nothing.
export async function actClaims(pool, session) {
  const { rows: [row] } = await pool.query(`SELECT actor FROM gregale_auth.impersonations
    WHERE session_id = $1 AND ended_at IS NULL AND expires_at > now() ORDER BY started_at DESC LIMIT 1`, [session])
  return row ? { act: { sub: row.actor } } : {}
}

const payloadOf = token => JSON.parse(Buffer.from(token.split('.')[1], 'base64url').toString('utf8'))

// startImpersonation opens the session and returns a link that hands it to
// the app (sessionFromUrl() reads it), with its ID and end.
export async function startImpersonation({ pool, admin, goTrueError, siteURL, keyId }, user, body) {
  const reason = typeof body?.reason === 'string' ? body.reason.trim() : ''
  const minutes = body?.minutes ?? limits.impersonationDefaultMinutes
  if (reason.length < 1 || reason.length > 200 || /[\x00-\x1f\x7f]/.test(reason)) throw new HTTPError(400, 'invalid_reason')
  if (!Number.isInteger(minutes) || minutes < 5 || minutes > limits.impersonationMaxMinutes) throw new HTTPError(400, 'invalid_minutes')
  const found = await admin('GET', `/admin/users/${user}`)
  if (found.status !== 200) throw goTrueError(found)
  const person = found.data
  // A magic link would confirm an unconfirmed address, so those are refused.
  if (!person?.email || !person.email_confirmed_at) throw new HTTPError(409, 'impersonation_needs_confirmed_email')
  if (person.banned_until && new Date(person.banned_until) > new Date()) throw new HTTPError(409, 'user_banned')
  const link = await admin('POST', '/admin/generate_link', { type: 'magiclink', email: person.email })
  if (link.status !== 200) throw goTrueError(link)
  const tokenHash = link.data?.hashed_token ?? link.data?.properties?.hashed_token
  if (typeof tokenHash !== 'string') throw new HTTPError(503, 'auth_unavailable')
  const verified = await admin('POST', '/verify', { type: 'magiclink', token_hash: tokenHash })
  if (verified.status !== 200 || typeof verified.data?.refresh_token !== 'string') throw goTrueError(verified)
  const session = payloadOf(verified.data.access_token).session_id
  const { rows: [row] } = await pool.query(`WITH started AS (
      INSERT INTO gregale_auth.impersonations (user_id, session_id, actor, reason, expires_at)
      VALUES ($1, $2, $3, $4, now() + make_interval(mins => $5)) RETURNING id, expires_at)
    UPDATE auth.sessions s SET not_after = started.expires_at FROM started WHERE s.id = $2 RETURNING started.id, started.expires_at`,
  [user, session, `admin:${keyId}`, reason, minutes])
  if (!row) throw new HTTPError(503, 'auth_unavailable')
  // The first token predates the record; the refreshed one carries act.
  const refreshed = await admin('POST', '/token?grant_type=refresh_token', { refresh_token: verified.data.refresh_token })
  if (refreshed.status !== 200) throw goTrueError(refreshed)
  const handoff = new URL(siteURL)
  handoff.hash = new URLSearchParams({
    access_token: refreshed.data.access_token, refresh_token: refreshed.data.refresh_token, expires_in: String(refreshed.data.expires_in),
    expires_at: String(refreshed.data.expires_at ?? Math.floor(Date.now() / 1000) + refreshed.data.expires_in), token_type: 'bearer', type: 'impersonation',
  }).toString()
  return [201, { id: row.id, session_id: session, user_id: user, expires_at: row.expires_at, url: handoff.toString() }]
}

// impersonationsRoute lists a user's (or everyone's) recent impersonations
// and ends one early by ending its session.
export async function impersonationsRoute({ pool }, method, parts, url) {
  if (parts.length === 1 && method === 'GET') {
    const user = url.searchParams.get('user')
    if (user !== null && !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(user)) throw new HTTPError(400, 'invalid_id')
    const { rows } = await pool.query(`SELECT i.id, i.user_id, u.email, i.session_id, i.actor, i.reason, i.started_at, i.expires_at, i.ended_at,
        (i.ended_at IS NULL AND i.expires_at > now() AND EXISTS (SELECT 1 FROM auth.sessions s WHERE s.id = i.session_id)) AS active
      FROM gregale_auth.impersonations i JOIN auth.users u ON u.id = i.user_id
      WHERE $1::uuid IS NULL OR i.user_id = $1 ORDER BY i.started_at DESC LIMIT $2`, [user, limits.adminPageSize])
    return [200, { impersonations: rows }]
  }
  if (parts.length === 2 && method === 'DELETE') {
    if (!/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(parts[1])) throw new HTTPError(400, 'invalid_id')
    const { rows: [row] } = await pool.query(`WITH ended AS (
        UPDATE gregale_auth.impersonations SET ended_at = coalesce(ended_at, now()) WHERE id = $1 RETURNING session_id)
      DELETE FROM auth.sessions s USING ended WHERE s.id = ended.session_id RETURNING s.id`, [parts[1]])
    const { rowCount } = row ? { rowCount: 1 } : await pool.query('SELECT 1 FROM gregale_auth.impersonations WHERE id = $1', [parts[1]])
    if (!rowCount) throw new HTTPError(404, 'not_found')
    return [204, undefined]
  }
  throw new HTTPError(404, 'not_found')
}
