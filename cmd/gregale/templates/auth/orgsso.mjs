import { randomBytes } from 'node:crypto'
import { resolveTxt } from 'node:dns/promises'
import { HTTPError } from './authz.mjs'
import { newScimToken } from './scim.mjs'
import { providerInput } from './sso.mjs'

// /authz/v1/orgs/{org}/domains and /sso: self-service organization SSO
// (ADR-1190, sql/0007). Owners and members whose role grants sso:manage
// prove an email domain with a DNS TXT record, then connect their own SAML
// identity provider for verified domains. The app owner turns it on with
// AUTH_ORG_SSO; GoTrue's admin API does the provider work, with the service
// token only this front mints.

export const txtName = domain => `_gregale-auth.${domain}`
export const txtValue = token => `gregale-auth-verification=${token}`

function domainView(row) {
  return { domain: row.domain, verified: row.verified_at !== null, verified_at: row.verified_at, record: { type: 'TXT', name: txtName(row.domain), value: txtValue(row.token) } }
}

// dnsHasToken looks the TXT record up with the system resolver; a lookup
// failure reads as not found.
export async function dnsHasToken(domain, token, lookup = resolveTxt) {
  let records
  try {
    records = await Promise.race([lookup(txtName(domain)), new Promise((_, reject) => setTimeout(() => reject(new Error('timeout')), 5000).unref())])
  } catch { return false }
  return records.some(parts => parts.join('') === txtValue(token))
}

function decodeDomain(value) {
  let domain
  try { domain = decodeURIComponent(value).toLowerCase() } catch { throw new HTTPError(400, 'invalid_domain') }
  if (domain.length > 253) throw new HTTPError(400, 'invalid_domain')
  return domain
}

function providerView(provider, role) {
  return { id: provider.id, entity_id: provider.saml?.entity_id ?? null, metadata_url: provider.saml?.metadata_url ?? null, domains: (provider.domains ?? []).map(d => d.domain), role }
}

// orgSSORoute answers /orgs/{org}/domains[...] and /orgs/{org}/sso[...];
// parts start after /orgs/{org}.
export async function orgSSORoute({ rows, admin, goTrueError, external, lookup }, org, method, parts, body) {
  const goTrue = async (verb, path, payload, ok = [200, 201]) => {
    const response = await admin(verb, path, payload)
    if (!ok.includes(response.status)) throw goTrueError(response)
    return response.data
  }
  if (parts[0] === 'domains') {
    if (parts.length === 1 && method === 'GET') return [200, { domains: (await rows('SELECT domain, token, verified_at FROM gregale_auth.list_org_domains($1)', [org])).map(domainView) }]
    if (parts.length === 1 && method === 'POST') {
      if (typeof body?.domain !== 'string') throw new HTTPError(400, 'invalid_domain')
      const [row] = await rows('SELECT domain, token, verified_at FROM gregale_auth.add_org_domain($1, $2, $3)', [org, body.domain, randomBytes(24).toString('base64url')])
      return [201, domainView(row)]
    }
    const domain = parts.length >= 2 ? decodeDomain(parts[1]) : null
    if (parts.length === 3 && parts[2] === 'verify' && method === 'POST') {
      const pending = (await rows('SELECT domain, token, verified_at FROM gregale_auth.list_org_domains($1)', [org])).find(row => row.domain === domain)
      if (!pending) throw new HTTPError(404, 'not_found')
      if (pending.verified_at === null && !(await dnsHasToken(domain, pending.token, lookup))) throw new HTTPError(409, 'txt_record_not_found')
      const [row] = await rows('SELECT domain, token, verified_at FROM gregale_auth.verify_org_domain($1, $2)', [org, domain])
      return [200, domainView(row)]
    }
    if (parts.length === 2 && method === 'DELETE') { await rows('SELECT gregale_auth.remove_org_domain($1, $2)', [org, domain]); return [204, null] }
    throw new HTTPError(404, 'not_found')
  }
  if (parts.length === 1 && method === 'GET') {
    const linked = await rows('SELECT provider_id, role FROM gregale_auth.org_sso_providers($1)', [org])
    const providers = await Promise.all(linked.map(async link => providerView(await goTrue('GET', `/admin/sso/providers/${link.provider_id}`), link.role)))
    return [200, { providers }]
  }
  if (parts.length === 1 && method === 'POST') {
    const input = providerInput(body ?? {}, true)
    const role = body?.role ?? 'member'
    await rows('SELECT gregale_auth.check_org_sso($1, NULL, $2, $3)', [org, input.domains, role])
    const provider = await goTrue('POST', '/admin/sso/providers', input)
    try {
      await rows('SELECT gregale_auth.link_sso_organization($1, $2, $3)', [provider.id, org, role])
    } catch (err) {
      // Nothing may be left half-made: remove the provider GoTrue created.
      await admin('DELETE', `/admin/sso/providers/${provider.id}`).catch(() => {})
      throw err
    }
    return [201, providerView(provider, role)]
  }
  const id = parts[1]
  if (!/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(id ?? '')) throw new HTTPError(404, 'not_found')
  if (parts.length === 2 && method === 'PATCH') {
    const input = providerInput(body ?? {}, false)
    const role = body?.role ?? null
    await rows('SELECT gregale_auth.check_org_sso($1, $2, $3, $4)', [org, id, input.domains ?? null, role])
    const provider = Object.keys(input).length ? await goTrue('PUT', `/admin/sso/providers/${id}`, input) : await goTrue('GET', `/admin/sso/providers/${id}`)
    if (role) await rows('SELECT gregale_auth.link_sso_organization($1, $2, $3)', [id, org, role])
    const [link] = (await rows('SELECT provider_id, role FROM gregale_auth.org_sso_providers($1)', [org])).filter(row => row.provider_id === id)
    return [200, providerView(provider, link?.role ?? role)]
  }
  if (parts.length === 2 && method === 'DELETE') {
    await rows('SELECT gregale_auth.check_org_sso($1, $2, NULL, NULL)', [org, id])
    await goTrue('DELETE', `/admin/sso/providers/${id}`)
    return [204, null]
  }
  if (parts.length === 3 && parts[2] === 'scim-token' && method === 'POST') {
    await rows('SELECT gregale_auth.check_org_sso($1, $2, NULL, NULL)', [org, id])
    const { token, hash } = newScimToken()
    await rows(`INSERT INTO gregale_auth.scim_tokens (provider_id, token_hash) VALUES ($1, $2)
      ON CONFLICT (provider_id) DO UPDATE SET token_hash = EXCLUDED.token_hash, created_at = now()`, [id, hash])
    return [201, { token, scim_url: `${external}/scim/v2` }]
  }
  throw new HTTPError(404, 'not_found')
}
