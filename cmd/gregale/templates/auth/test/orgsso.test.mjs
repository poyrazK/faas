import test from 'node:test'
import assert from 'node:assert/strict'
import { dnsHasToken, orgSSORoute, txtName, txtValue } from '../orgsso.mjs'
import { route } from '../authz.mjs'

const org = '7b1b2c3d-0000-4000-8000-000000000002'
const providerID = '6a1b2c3d-0000-4000-8000-000000000001'

test('the TXT record proves control of the domain', async () => {
  assert.equal(txtName('acme.com'), '_gregale-auth.acme.com')
  assert.equal(txtValue('t'), 'gregale-auth-verification=t')
  const lookup = async name => name === '_gregale-auth.acme.com' ? [['v=spf1 -all'], ['gregale-auth-verification=', 'tok']] : Promise.reject(new Error('ENOTFOUND'))
  assert.equal(await dnsHasToken('acme.com', 'tok', lookup), true, 'TXT strings split in chunks are joined')
  assert.equal(await dnsHasToken('acme.com', 'other', lookup), false)
  assert.equal(await dnsHasToken('globex.com', 'tok', lookup), false)
})

// fakes record the SQL the route runs (the functions enforce permissions)
// and answer GoTrue's admin API.
function fakes({ linkFails = false } = {}) {
  const calls = [], domains = new Map(), links = new Map(), deleted = []
  const rows = async (sql, args) => {
    calls.push(sql.match(/gregale_auth\.(\w+)|INSERT INTO gregale_auth\.(\w+)/).slice(1).find(Boolean))
    if (sql.includes('add_org_domain')) { const row = { domain: args[1].toLowerCase(), token: args[2], verified_at: null }; domains.set(row.domain, row); return [row] }
    if (sql.includes('list_org_domains')) return [...domains.values()]
    if (sql.includes('verify_org_domain')) { const row = domains.get(args[1]); row.verified_at = 'now'; return [row] }
    if (sql.includes('remove_org_domain')) { domains.delete(args[1]); return [] }
    if (sql.includes('check_org_sso')) {
      // A new provider's check passes (org, domains, role); others (org, id, domains, role).
      const [provider, requested] = sql.includes('NULL, $2') ? [null, args[1]] : [args[1], args[2]]
      if (provider && !links.has(provider)) throw Object.assign(new Error('not_found'), { status: 404 })
      if (requested?.some(d => domains.get(d)?.verified_at === null || !domains.has(d))) throw Object.assign(new Error('domain_not_verified'), { status: 403 })
      return []
    }
    if (sql.includes('link_sso_organization')) { if (linkFails) throw new Error('link failed'); links.set(args[0], args[2]); return [] }
    if (sql.includes('org_sso_providers')) return [...links].map(([provider_id, role]) => ({ provider_id, role }))
    if (sql.includes('scim_tokens')) return []
    throw new Error('unexpected SQL ' + sql)
  }
  const provider = { id: providerID, saml: { entity_id: 'https://idp.acme.com' }, domains: [{ domain: 'acme.com' }] }
  const admin = async (method, path) => {
    if (method === 'DELETE') { deleted.push(path); return { status: 200, data: {} } }
    return { status: method === 'POST' ? 201 : 200, data: provider }
  }
  const goTrueError = () => new Error('gotrue')
  const lookup = async () => [[txtValue([...domains.values()][0]?.token)]]
  const call = (method, path, body) => orgSSORoute({ rows, admin, goTrueError, external: 'https://shop-auth.gregale.app', lookup }, org, method, path.split('/').filter(Boolean), body)
  return { call, calls, domains, links, deleted }
}

test('owners verify a domain and connect an identity provider for it', async () => {
  const { call, domains, links } = fakes()
  const [status, added] = await call('POST', '/domains', { domain: 'Acme.com' })
  assert.equal(status, 201)
  assert.deepEqual(added.record, { type: 'TXT', name: '_gregale-auth.acme.com', value: `gregale-auth-verification=${domains.get('acme.com').token}` })
  assert.match(domains.get('acme.com').token, /^[A-Za-z0-9_-]{32}$/)
  await assert.rejects(call('POST', '/sso', { metadata_url: 'https://idp.acme.com/metadata', domains: ['acme.com'] }), { message: 'domain_not_verified' })
  const [, verified] = await call('POST', '/domains/acme.com/verify')
  assert.equal(verified.verified, true)
  const [created, provider] = await call('POST', '/sso', { metadata_url: 'https://idp.acme.com/metadata', domains: ['acme.com'], role: 'admin' })
  assert.equal(created, 201)
  assert.deepEqual(provider, { id: providerID, entity_id: 'https://idp.acme.com', metadata_url: null, domains: ['acme.com'], role: 'admin' })
  assert.equal(links.get(providerID), 'admin')
  assert.deepEqual((await call('GET', '/sso'))[1].providers.map(p => p.role), ['admin'])
  const [, scim] = await call('POST', `/sso/${providerID}/scim-token`)
  assert.match(scim.token, /^gscim_/)
  assert.equal((await call('PATCH', `/sso/${providerID}`, { role: 'member' }))[1].role, 'member')
  assert.deepEqual(await call('DELETE', `/sso/${providerID}`), [204, null])
  await assert.rejects(call('PATCH', '/sso/not-a-uuid', {}), { status: 404 })
})

test('an unproven domain is not verified and a failed link leaves no provider', async () => {
  const { call, deleted } = fakes({ linkFails: true })
  await call('POST', '/domains', { domain: 'acme.com' })
  const unproven = fakes()
  await unproven.call('POST', '/domains', { domain: 'acme.com' })
  const wrong = async () => [['gregale-auth-verification=someone-else']]
  await assert.rejects(orgSSORoute({ rows: async (sql, args) => [{ domain: 'acme.com', token: 'x'.repeat(32), verified_at: null }], admin: async () => ({}), goTrueError: () => new Error(), external: '', lookup: wrong }, org, 'POST', ['domains', 'acme.com', 'verify']), { status: 409, code: 'txt_record_not_found' })
  await call('POST', '/domains/acme.com/verify')
  await assert.rejects(call('POST', '/sso', { metadata_url: 'https://idp.acme.com/metadata', domains: ['acme.com'] }), { message: 'link failed' })
  assert.deepEqual(deleted, [`/admin/sso/providers/${providerID}`])
})

test('organization SSO stays off until the app owner turns it on', async () => {
  const pool = { connect: async () => { throw new Error('no database needed') } }
  await assert.rejects(route(pool, { sub: 'u1' }, 'POST', `/orgs/${org}/domains`, { domain: 'acme.com' }, {}), { status: 403, code: 'org_sso_disabled' })
  await assert.rejects(route(pool, { sub: 'u1' }, 'GET', `/orgs/${org}/sso`, undefined, {}), { status: 403, code: 'org_sso_disabled' })
})
