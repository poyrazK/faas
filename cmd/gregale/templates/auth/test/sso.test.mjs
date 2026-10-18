import test from 'node:test'
import assert from 'node:assert/strict'
import { generateKeyPairSync } from 'node:crypto'
import { providerInput, ssoRoute } from '../sso.mjs'
import { runtimeConfig, samlEnv } from '../config.mjs'
import { limits } from '../config.mjs'

const providerID = '6a1b2c3d-0000-4000-8000-000000000001'
const orgID = '7b1b2c3d-0000-4000-8000-000000000002'

function samlKey(bits = 2048) {
  const { privateKey } = generateKeyPairSync('rsa', { modulusLength: bits, publicExponent: 65537 })
  return privateKey.export({ format: 'der', type: 'pkcs1' }).toString('base64')
}

test('SSO needs a sealed RSA key GoTrue accepts and reports what an identity provider needs', () => {
  assert.deepEqual(samlEnv({}), { GOTRUE_SAML_ENABLED: 'false' })
  const key = samlKey()
  assert.deepEqual(samlEnv({ AUTH_SSO: '1', AUTH_SAML_PRIVATE_KEY: key }), { GOTRUE_SAML_ENABLED: 'true', GOTRUE_SAML_PRIVATE_KEY: key })
  // Turned off, the key stays sealed and GoTrue does not get it.
  assert.deepEqual(samlEnv({ AUTH_SSO: '0', AUTH_SAML_PRIVATE_KEY: key }), { GOTRUE_SAML_ENABLED: 'false' })
  assert.throws(() => samlEnv({ AUTH_SSO: '1' }), /requires AUTH_SAML_PRIVATE_KEY/)
  assert.throws(() => samlEnv({ AUTH_SSO: '1', AUTH_SAML_PRIVATE_KEY: 'not base64!' }), /requires AUTH_SAML_PRIVATE_KEY/)
  assert.throws(() => samlEnv({ AUTH_SSO: '1', AUTH_SAML_PRIVATE_KEY: samlKey(1024) }), /at least 2048 bits/)
  const ec = generateKeyPairSync('ec', { namedCurve: 'P-256' }).privateKey.export({ format: 'der', type: 'sec1' }).toString('base64')
  assert.throws(() => samlEnv({ AUTH_SSO: '1', AUTH_SAML_PRIVATE_KEY: ec }), /AUTH_SAML_PRIVATE_KEY/)
})

test('SSO settings name the service provider and never the key', () => {
  const base = {
    AUTH_EXTERNAL_URL: 'https://shop-auth.gregale.app', AUTH_SITE_URL: 'https://shop.example.com', AUTH_JWT_SECRET: 'x'.repeat(32), DATABASE_URL: 'postgres://auth@localhost/app',
    AUTH_JWT_KEY_0A1B2C3D: JSON.stringify({ kty: 'EC', crv: 'P-256', alg: 'ES256', kid: 'gregale-0a1b2c3d', ...generateKeyPairSync('ec', { namedCurve: 'P-256' }).privateKey.export({ format: 'jwk' }) }),
    AUTH_JWT_SIGNER_0A1B2C3D: '1700000000',
  }
  const key = samlKey()
  const config = runtimeConfig({ ...base, AUTH_SSO: '1', AUTH_SAML_PRIVATE_KEY: key })
  assert.deepEqual(config.settings.sso, { enabled: true, entity_id: 'https://shop-auth.gregale.app/auth/v1/sso/saml/metadata', acs_url: 'https://shop-auth.gregale.app/auth/v1/sso/saml/acs', self_service: false })
  assert.equal(runtimeConfig({ ...base, AUTH_SSO: '1', AUTH_SAML_PRIVATE_KEY: key, AUTH_ORG_SSO: '1' }).settings.sso.self_service, true)
  assert.equal(runtimeConfig({ ...base, AUTH_ORG_SSO: '1' }).orgSSO, false)
  assert.ok(!JSON.stringify(config.settings).includes(key.slice(0, 40)))
  assert.deepEqual(runtimeConfig(base).settings.sso, { enabled: false, entity_id: null, acs_url: null, self_service: false })
  assert.throws(() => runtimeConfig({ ...base, GOTRUE_SAML_ENABLED: 'true' }), /managed by Gregale/)
  assert.throws(() => runtimeConfig({ ...base, AUTH_SSO: 'yes' }), /Invalid AUTH_SSO/)
})

test('provider input is checked before GoTrue sees it', () => {
  assert.deepEqual(providerInput({ metadata_url: 'https://idp.example.com/metadata', domains: ['Acme.com', 'acme.com', 'acme.co.uk'] }, true),
    { type: 'saml', metadata_url: 'https://idp.example.com/metadata', domains: ['acme.com', 'acme.co.uk'] })
  assert.deepEqual(providerInput({ domains: ['acme.io'] }, false), { domains: ['acme.io'] })
  assert.deepEqual(providerInput({}, false), {})
  for (const [body, code] of [
    [{ domains: ['acme.com'] }, 'invalid_metadata'],
    [{ metadata_url: 'http://idp.example.com/m', domains: ['acme.com'] }, 'invalid_metadata'],
    [{ metadata_url: 'https://u:p@idp.example.com/m', domains: ['acme.com'] }, 'invalid_metadata'],
    [{ metadata_url: 'https://idp.example.com/m', metadata_xml: '<EntityDescriptor/>', domains: ['acme.com'] }, 'invalid_metadata'],
    [{ metadata_xml: '<html/>', domains: ['acme.com'] }, 'invalid_metadata'],
    [{ metadata_xml: '<EntityDescriptor>' + 'x'.repeat(limits.ssoMetadataBytes) + '</EntityDescriptor>', domains: ['acme.com'] }, 'invalid_metadata'],
    [{ metadata_url: 'https://idp.example.com/m' }, 'invalid_domains'],
    [{ metadata_url: 'https://idp.example.com/m', domains: [] }, 'invalid_domains'],
    [{ metadata_url: 'https://idp.example.com/m', domains: ['localhost'] }, 'invalid_domains'],
    [{ metadata_url: 'https://idp.example.com/m', domains: ['acme.com/x'] }, 'invalid_domains'],
  ]) {
    assert.throws(() => providerInput(body, true), { code }, JSON.stringify(body))
  }
})

// fakes answer the GoTrue admin calls and gregale_auth queries ssoRoute makes.
function fakes({ disabled = false } = {}) {
  const providers = new Map(), linksTable = new Map(), calls = [], tokens = new Map()
  const goTrueError = ({ status, data }) => Object.assign(new Error(data?.error_code ?? 'x'), { status: status === 422 ? 409 : 400, code: data?.error_code ?? 'invalid_request' })
  const admin = async (method, path, body) => {
    calls.push([method, path, body])
    if (disabled) return { status: 404, data: { error_code: 'saml_provider_disabled' } }
    const id = path.split('/')[4]
    if (method === 'GET' && !id) return { status: 200, data: { items: [...providers.values()] } }
    if (method === 'POST') {
      if (body.domains.includes('taken.com')) return { status: 400, data: { error_code: 'sso_domain_already_exists' } }
      const provider = { id: providerID, saml: { entity_id: 'https://idp.example.com', metadata_url: body.metadata_url }, domains: body.domains.map(domain => ({ domain })), created_at: 'now' }
      providers.set(provider.id, provider)
      return { status: 201, data: provider }
    }
    if (!providers.has(id)) return { status: 404, data: { error_code: 'sso_provider_not_found' } }
    if (method === 'DELETE') { providers.delete(id); linksTable.delete(id); return { status: 200, data: {} } }
    if (method === 'PUT') Object.assign(providers.get(id), body.domains ? { domains: body.domains.map(domain => ({ domain })) } : {})
    return { status: 200, data: providers.get(id) }
  }
  const query = async (sql, args) => {
    if (sql.includes('FROM gregale_auth.organizations WHERE')) return args[0] === orgID || args[0] === 'acme' ? [{ id: orgID, slug: 'acme' }] : []
    if (sql.includes('FROM gregale_auth.roles')) return ['member', 'admin', 'editor'].includes(args[0]) ? [{ ok: 1 }] : []
    if (sql.includes('link_sso_organization')) {
      if (args[1] === null) linksTable.delete(args[0])
      else linksTable.set(args[0], { provider_id: args[0], org_id: args[1], slug: 'acme', role: args[2] })
      return [{ joined: args[1] === null ? 0 : 3 }]
    }
    if (sql.includes('FROM gregale_auth.sso_organizations')) return [...linksTable.values()]
    if (sql.startsWith('SELECT id FROM auth.sso_providers')) return providers.has(args[0]) ? [{ id: args[0] }] : []
    if (sql.startsWith('INSERT INTO gregale_auth.scim_tokens')) { tokens.set(args[0], args[1]); return [] }
    if (sql.startsWith('DELETE FROM gregale_auth.scim_tokens')) { tokens.delete(args[0]); return [] }
    throw new Error('unexpected SQL ' + sql)
  }
  return { deps: { query, admin, goTrueError, external: 'https://shop-auth.gregale.app' }, providers, linksTable, calls, tokens }
}

test('owners register identity providers and link them to an organization', async () => {
  const { deps, linksTable, calls } = fakes()
  const route = (method, path, body) => ssoRoute(deps, method, path.split('/').filter(Boolean), body)
  const [status, created] = await route('POST', '/sso', { metadata_url: 'https://idp.example.com/metadata', domains: ['acme.com'], organization: 'acme', role: 'editor' })
  assert.equal(status, 201)
  assert.deepEqual(created, { provider: { id: providerID, entity_id: 'https://idp.example.com', metadata_url: 'https://idp.example.com/metadata', domains: ['acme.com'], disabled: false, created_at: 'now', organization: { id: orgID, slug: 'acme', role: 'editor' } }, members_added: 3 })
  assert.deepEqual(calls[0], ['POST', '/admin/sso/providers', { type: 'saml', metadata_url: 'https://idp.example.com/metadata', domains: ['acme.com'] }])
  assert.deepEqual((await route('GET', '/sso'))[1].providers.map(p => [p.id, p.organization?.slug]), [[providerID, 'acme']])
  // Unlinking keeps the provider; members keep their memberships.
  const [, unlinked] = await route('PATCH', `/sso/${providerID}`, { organization: null, domains: ['acme.com', 'acme.io'] })
  assert.equal(unlinked.provider.organization, null)
  assert.deepEqual(unlinked.provider.domains, ['acme.com', 'acme.io'])
  assert.equal(linksTable.size, 0)
  assert.deepEqual(await route('DELETE', `/sso/${providerID}`), [200, { deleted: true }])
})

test('nothing changes when the organization, role or provider is wrong', async () => {
  const { deps, calls } = fakes()
  const route = (method, path, body) => ssoRoute(deps, method, path.split('/').filter(Boolean), body)
  const valid = { metadata_url: 'https://idp.example.com/metadata', domains: ['acme.com'] }
  await assert.rejects(route('POST', '/sso', { ...valid, organization: 'nope' }), { status: 404, code: 'organization_not_found' })
  await assert.rejects(route('POST', '/sso', { ...valid, organization: 'acme', role: 'owner' }), { status: 400, code: 'invalid_role' })
  await assert.rejects(route('POST', '/sso', { ...valid, organization: 'acme', role: 'ghost' }), { status: 400, code: 'unknown_role' })
  assert.equal(calls.length, 0)
  await assert.rejects(route('POST', '/sso', { ...valid, domains: ['taken.com'] }), { code: 'sso_domain_already_exists' })
  await assert.rejects(route('PATCH', '/sso/not-a-uuid', {}), { status: 404 })
  await assert.rejects(route('DELETE', `/sso/${providerID}`), { code: 'sso_provider_not_found' })
  const off = fakes({ disabled: true })
  await assert.rejects(ssoRoute(off.deps, 'GET', ['sso']), { status: 409, code: 'sso_disabled' })
})

test('a SCIM token is issued per provider, shown once and stored hashed', async () => {
  const { deps, tokens } = fakes()
  const route = (method, path, body) => ssoRoute(deps, method, path.split('/').filter(Boolean), body)
  await route('POST', '/sso', { metadata_url: 'https://idp.example.com/metadata', domains: ['acme.com'] })
  const [status, issued] = await route('POST', `/sso/${providerID}/scim-token`)
  assert.equal(status, 201)
  assert.match(issued.token, /^gscim_[A-Za-z0-9_-]{43}$/)
  assert.equal(issued.scim_url, 'https://shop-auth.gregale.app/scim/v2')
  assert.equal(tokens.get(providerID).length, 32)
  assert.notDeepEqual(tokens.get(providerID), Buffer.from(issued.token))
  await assert.rejects(route('POST', '/sso/7b1b2c3d-0000-4000-8000-000000000009/scim-token'), { status: 404 })
  await assert.rejects(route('POST', `/sso/${providerID}/other`), { status: 404 })
  assert.deepEqual(await route('DELETE', `/sso/${providerID}/scim-token`), [200, { revoked: true }])
  assert.equal(tokens.size, 0)
})
