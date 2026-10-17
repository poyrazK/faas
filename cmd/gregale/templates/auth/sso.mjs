import { limits } from './config.mjs'
import { HTTPError } from './authz.mjs'
import { newScimToken } from './scim.mjs'

// /admin/v1/sso: organization single sign-on (ADR-1190). The owner registers
// SAML identity providers through GoTrue's admin API and may link each one to
// an organization: users who sign in through it join that organization with
// the linked role (sql/0005). Turning SSO on (AUTH_SSO) is an app setting;
// without it GoTrue answers saml_provider_disabled.

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
const domainName = /^(?=.{1,253}$)([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$/

function ssoError(response, goTrueError) {
  if (response.status === 404 && response.data?.error_code === 'saml_provider_disabled') return new HTTPError(409, 'sso_disabled')
  return goTrueError(response)
}

function providerView(provider, links) {
  const link = links.get(provider.id)
  return {
    id: provider.id, entity_id: provider.saml?.entity_id ?? null, metadata_url: provider.saml?.metadata_url ?? null,
    domains: (provider.domains ?? []).map(d => d.domain), disabled: provider.disabled === true, created_at: provider.created_at ?? null,
    organization: link ? { id: link.org_id, slug: link.slug, role: link.role } : null,
  }
}

// providerInput checks what GoTrue would also check, so a mistake is
// reported before any change. Domains are required: the hosted pages and
// invitation checks find a provider by its users' email domains.
export function providerInput(body, creating) {
  const out = creating ? { type: 'saml' } : {}
  const hasURL = body.metadata_url !== undefined, hasXML = body.metadata_xml !== undefined
  if (hasURL && hasXML) throw new HTTPError(400, 'invalid_metadata')
  if (hasURL) {
    let url
    try { url = new URL(body.metadata_url) } catch { throw new HTTPError(400, 'invalid_metadata') }
    if (url.protocol !== 'https:' || url.username || url.password || body.metadata_url.length > 2048) throw new HTTPError(400, 'invalid_metadata')
    out.metadata_url = body.metadata_url
  } else if (hasXML) {
    if (typeof body.metadata_xml !== 'string' || body.metadata_xml.length > limits.ssoMetadataBytes || !/EntityDescriptor/.test(body.metadata_xml)) throw new HTTPError(400, 'invalid_metadata')
    out.metadata_xml = body.metadata_xml
  } else if (creating) {
    throw new HTTPError(400, 'invalid_metadata')
  }
  if (body.domains !== undefined || creating) {
    const domains = Array.isArray(body.domains) ? [...new Set(body.domains.map(d => typeof d === 'string' ? d.toLowerCase() : ''))] : []
    if (domains.length === 0 || domains.length > limits.ssoDomains || !domains.every(d => domainName.test(d))) throw new HTTPError(400, 'invalid_domains')
    out.domains = domains
  }
  return out
}

// organizationInput resolves {organization: id or slug, role} before any
// change; null unlinks. Identity providers never grant owner.
async function organizationInput(query, body) {
  if (body.organization === undefined) return undefined
  if (body.organization === null) return null
  if (typeof body.organization !== 'string' || body.organization.length > 64) throw new HTTPError(400, 'invalid_organization')
  const role = body.role ?? 'member'
  if (typeof role !== 'string' || role === 'owner') throw new HTTPError(400, 'invalid_role')
  const [org] = await query('SELECT id, slug FROM gregale_auth.organizations WHERE id::text = $1 OR slug = $1', [body.organization])
  if (!org) throw new HTTPError(404, 'organization_not_found')
  const [known] = await query('SELECT 1 AS ok FROM gregale_auth.roles WHERE name = $1', [role])
  if (!known) throw new HTTPError(400, 'unknown_role')
  return { id: org.id, role }
}

async function links(query) {
  const rows = await query('SELECT s.provider_id, s.org_id, o.slug, s.role FROM gregale_auth.sso_organizations s JOIN gregale_auth.organizations o ON o.id = s.org_id')
  return new Map(rows.map(row => [row.provider_id, row]))
}

async function link(query, provider, org) {
  if (org === undefined) return 0
  const [{ joined }] = await query('SELECT gregale_auth.link_sso_organization($1, $2, $3) AS joined', [provider, org?.id ?? null, org?.role ?? null])
  return joined
}

// ssoRoute answers /admin/v1/sso[/<id>]; parts are the path's segments.
export async function ssoRoute({ query, admin, goTrueError, external }, method, parts, body) {
  const id = parts[1]
  if (parts.length > 3 || (id !== undefined && !uuid.test(id)) || (parts.length === 3 && parts[2] !== 'scim-token')) throw new HTTPError(404, 'not_found')
  // A SCIM token lets the provider's identity provider provision its people
  // (scim.mjs); creating one replaces the previous token.
  if (parts[2] === 'scim-token' && method === 'POST') {
    const [known] = await query('SELECT id FROM auth.sso_providers WHERE id = $1', [id])
    if (!known) throw new HTTPError(404, 'not_found')
    const { token, hash } = newScimToken()
    await query(`INSERT INTO gregale_auth.scim_tokens (provider_id, token_hash) VALUES ($1, $2)
      ON CONFLICT (provider_id) DO UPDATE SET token_hash = EXCLUDED.token_hash, created_at = now()`, [id, hash])
    return [201, { token, scim_url: `${external}/scim/v2` }]
  }
  if (parts[2] === 'scim-token' && method === 'DELETE') {
    await query('DELETE FROM gregale_auth.scim_tokens WHERE provider_id = $1', [id])
    return [200, { revoked: true }]
  }
  if (parts[2]) throw new HTTPError(404, 'not_found')
  const call = async (verb, path, payload, ok = [200, 201]) => {
    const response = await admin(verb, path, payload)
    if (!ok.includes(response.status)) throw ssoError(response, goTrueError)
    return response.data
  }
  if (!id && method === 'GET') {
    const data = await call('GET', '/admin/sso/providers')
    const linked = await links(query)
    return [200, { providers: (data?.items ?? []).map(p => providerView(p, linked)) }]
  }
  if (!id && method === 'POST') {
    if (!body || typeof body !== 'object') throw new HTTPError(400, 'invalid_request')
    const input = providerInput(body, true)
    const org = await organizationInput(query, body)
    const provider = await call('POST', '/admin/sso/providers', input)
    const joined = await link(query, provider.id, org)
    return [201, { provider: providerView(provider, await links(query)), members_added: joined }]
  }
  if (id && method === 'PATCH') {
    if (!body || typeof body !== 'object') throw new HTTPError(400, 'invalid_request')
    const input = providerInput(body, false)
    const org = await organizationInput(query, body)
    const provider = Object.keys(input).length ? await call('PUT', `/admin/sso/providers/${id}`, input) : await call('GET', `/admin/sso/providers/${id}`)
    const joined = await link(query, provider.id, org)
    return [200, { provider: providerView(provider, await links(query)), members_added: joined }]
  }
  if (id && method === 'DELETE') {
    // Deleting the provider removes its link (sql/0005); its users stay,
    // keep their memberships and can no longer sign in through it.
    await call('DELETE', `/admin/sso/providers/${id}`)
    return [200, { deleted: true }]
  }
  throw new HTTPError(404, 'not_found')
}
