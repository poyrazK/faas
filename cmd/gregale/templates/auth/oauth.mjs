import { HTTPError } from './authz.mjs'
import { consentPath, limits, oauthAudience } from './config.mjs'

// OAuth 2.1 authorization server (ADR-1190). GoTrue v2.197.0 registers
// clients and issues codes and tokens; the front adds what makes it safe to
// offer to third parties:
//  - GoTrue sends people to SITE_URL + consentPath to approve a client; the
//    front redirects them to the hosted consent page on this host instead.
//  - A client's token reaches only userinfo and its own sign-out here; every
//    other route on this host is for the application's own sessions.
//  - GoTrue's custom access token hook gives a client's token the audience
//    of the resource (RFC 8707) it asked for, or oauthAudience, so a verifier
//    set up for this app's own tokens ("authenticated") never accepts one.
//  - Dynamic registration stops at limits.oauthClients clients.

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

// consentLocation rewrites GoTrue's redirect to the consent page; any other
// location comes back unchanged.
export function consentLocation(location, site, external) {
  const prefix = `${site.replace(/\/+$/, '')}${consentPath}?`
  if (typeof location !== 'string' || !location.startsWith(prefix)) return location
  const id = new URLSearchParams(location.slice(prefix.length)).get('authorization_id')
  return id ? `${external}/ui/consent?authorization_id=${encodeURIComponent(id)}` : location
}

// bearerClaims reads a bearer token's claims without verifying them. They
// only ever refuse a request, never allow one.
export function bearerClaims(header) {
  const match = /^Bearer [A-Za-z0-9_-]+\.([A-Za-z0-9_-]+)\.[A-Za-z0-9_-]+$/.exec(header ?? '')
  if (!match) return null
  try { return JSON.parse(Buffer.from(match[1], 'base64url').toString('utf8')) } catch { return null }
}

export function isClientToken(claims) {
  return typeof claims?.client_id === 'string' && claims.client_id !== ''
}

// clientTokenAllowed lists what a client's token may call on this host:
// the userinfo endpoint, ending its own session (GoTrue's default sign-out
// scope is every session of the user), and checking a permission of its
// user, which a resource server such as an MCP server needs.
export function clientTokenAllowed(method, url) {
  return (method === 'GET' && url === '/auth/v1/oauth/userinfo') || (method === 'POST' && url === '/auth/v1/logout?scope=local') ||
    (method === 'POST' && url === '/authz/v1/authorize')
}

// wellKnownPath maps the RFC 8414 and OIDC discovery URLs for the issuer
// <host>/auth/v1 onto GoTrue's root; it returns null for anything else.
export function wellKnownPath(url) {
  const match = /^\/\.well-known\/(oauth-authorization-server|openid-configuration)\/auth\/v1$/.exec(url)
  return match ? `/.well-known/${match[1]}` : null
}

// accessTokenClaims answers GoTrue's custom access token hook. The
// application's own tokens pass unchanged.
export async function accessTokenClaims(pool, payload) {
  const claims = payload?.claims
  if (!claims || typeof claims !== 'object' || Array.isArray(claims)) throw new HTTPError(400, 'invalid_hook_payload')
  // GoTrue writes a single audience as a string, but only from its first
  // signature on: the first hook call after every start sends an array.
  const aud = Array.isArray(claims.aud) && claims.aud.length === 1 ? claims.aud[0] : claims.aud
  if (!isClientToken(claims)) return { claims: { ...claims, aud } }
  return { claims: { ...claims, aud: (await sessionResource(pool, claims)) ?? oauthAudience } }
}

// sessionResource is the resource a client's session was authorized for.
// GoTrue drops the authorization when it exchanges the code, so the first
// token of a session records it for every refresh after.
async function sessionResource(pool, { session_id: session, client_id: client, sub: user }) {
  if (![session, client, user].every(id => uuid.test(id ?? ''))) throw new HTTPError(400, 'invalid_hook_payload')
  const remembered = await pool.query('SELECT resource FROM gregale_auth.oauth_session_resources WHERE session_id = $1', [session])
  if (remembered.rows.length) return remembered.rows[0].resource
  // During the exchange GoTrue still holds the approved authorization it is
  // using. A second one exists only if the person approved this client
  // again within the code's lifetime; then both must name one resource.
  const pending = await pool.query(`SELECT DISTINCT resource FROM auth.oauth_authorizations
    WHERE client_id = $1 AND user_id = $2 AND status = 'approved' AND authorization_code IS NOT NULL AND expires_at > now()`, [client, user])
  if (pending.rows.length > 1) throw new HTTPError(409, 'ambiguous_resource')
  if (pending.rows.length === 0) return null
  const { resource } = pending.rows[0]
  await pool.query('INSERT INTO gregale_auth.oauth_session_resources (session_id, client_id, resource) VALUES ($1, $2, $3) ON CONFLICT (session_id) DO NOTHING', [session, client, resource])
  return resource
}

// registrationOpen checks the bound on registered clients before GoTrue
// takes a dynamic registration.
export async function registrationOpen(pool) {
  const { rows } = await pool.query('SELECT count(*)::int AS clients FROM auth.oauth_clients WHERE deleted_at IS NULL')
  return rows[0].clients < limits.oauthClients
}

// authorizationResource adds the requested resource to the consent page's
// view of an authorization; GoTrue's response leaves it out.
export async function authorizationResource(pool, id) {
  const { rows } = await pool.query('SELECT resource FROM auth.oauth_authorizations WHERE authorization_id = $1', [id])
  return rows[0]?.resource ?? null
}

export const authorizationDetails = /^\/oauth\/authorizations\/([A-Za-z0-9_-]{1,128})$/

// clientInput checks an owner's client before GoTrue's admin API does, so
// a mistake is reported without a change. GoTrue validates redirect URIs.
export function clientInput(body, creating) {
  const out = creating ? { registration_type: 'manual' } : {}
  if (body === null || typeof body !== 'object' || Array.isArray(body)) throw new HTTPError(400, 'invalid_client')
  for (const [name, value] of Object.entries(body)) {
    if (name === 'client_name' || name === 'client_uri' || name === 'logo_uri') {
      if (typeof value !== 'string' || value.length > (name === 'client_name' ? 1024 : 2048)) throw new HTTPError(400, 'invalid_client')
      out[name] = value
    } else if (name === 'redirect_uris') {
      if (!Array.isArray(value) || value.length === 0 || value.length > 10 || !value.every(uri => typeof uri === 'string' && uri.length <= 2048)) throw new HTTPError(400, 'invalid_redirect_uris')
      out.redirect_uris = value
    } else if (name === 'client_type' && creating) {
      if (value !== 'public' && value !== 'confidential') throw new HTTPError(400, 'invalid_client_type')
      out.client_type = value
    } else {
      throw new HTTPError(400, 'invalid_client')
    }
  }
  if (creating && !out.redirect_uris) throw new HTTPError(400, 'invalid_redirect_uris')
  return out
}

function clientView(client) {
  return {
    id: client.client_id, name: client.client_name ?? null, type: client.client_type, registration: client.registration_type,
    redirect_uris: client.redirect_uris ?? [], client_uri: client.client_uri ?? null, logo_uri: client.logo_uri ?? null,
    token_endpoint_auth_method: client.token_endpoint_auth_method ?? null, created_at: client.created_at ?? null,
  }
}

// oauthRoute serves /admin/v1/oauth/clients: the owner's clients through
// GoTrue's admin API. A secret leaves only on create and on regeneration.
export async function oauthRoute({ admin, goTrueError }, method, parts, body) {
  if (parts[1] !== 'clients') throw new HTTPError(404, 'not_found')
  const id = parts[2]
  if (id !== undefined && !uuid.test(id)) throw new HTTPError(400, 'invalid_id')
  const call = async (verb, path, payload, expected = [200]) => {
    const response = await admin(verb, path, payload)
    if (response.status === 404 && response.data?.error_code === 'feature_disabled') throw new HTTPError(409, 'oauth_server_disabled')
    if (!expected.includes(response.status)) throw goTrueError(response)
    return response.data
  }
  if (parts.length === 2 && method === 'GET') return [200, { clients: ((await call('GET', '/admin/oauth/clients'))?.clients ?? []).map(clientView) }]
  if (parts.length === 2 && method === 'POST') {
    const client = await call('POST', '/admin/oauth/clients', clientInput(body, true), [200, 201])
    return [201, { client: clientView(client), client_secret: client.client_secret ?? null }]
  }
  if (parts.length === 3 && method === 'GET') return [200, { client: clientView(await call('GET', `/admin/oauth/clients/${id}`)) }]
  if (parts.length === 3 && method === 'PATCH') {
    const change = clientInput(body, false)
    if (Object.keys(change).length === 0) throw new HTTPError(400, 'invalid_client')
    return [200, { client: clientView(await call('PUT', `/admin/oauth/clients/${id}`, change)) }]
  }
  if (parts.length === 3 && method === 'DELETE') {
    await call('DELETE', `/admin/oauth/clients/${id}`, undefined, [200, 204])
    return [200, { id, deleted: true }]
  }
  if (parts.length === 4 && parts[3] === 'regenerate-secret' && method === 'POST') {
    const client = await call('POST', `/admin/oauth/clients/${id}/regenerate_secret`, {})
    return [200, { client: clientView(client), client_secret: client.client_secret }]
  }
  throw new HTTPError(404, 'not_found')
}
