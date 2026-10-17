import test from 'node:test'
import assert from 'node:assert/strict'
import { generateKeyPairSync } from 'node:crypto'
import { authzConfig, characterClasses, databaseURL, migrationEnv, runtimeConfig, signingKeys } from '../config.mjs'

// keySecret is one AUTH_JWT_KEY_<ID> secret as `gregale auth` writes it.
function keySecret(id) {
  const { privateKey } = generateKeyPairSync('ec', { namedCurve: 'P-256' })
  return { [`AUTH_JWT_KEY_${id}`]: JSON.stringify({ ...privateKey.export({ format: 'jwk' }), alg: 'ES256', kid: `gregale-${id.toLowerCase()}` }) }
}

function env(overrides = {}) {
  return {
    DATABASE_URL: 'postgresql://gregale_auth_x:secret@db.example.com:5432/app?sslmode=require',
    AUTH_EXTERNAL_URL: 'https://shop-auth.gregale.app',
    AUTH_SITE_URL: 'https://shop.example.com',
    AUTH_JWT_SECRET: 'x'.repeat(48),
    ...keySecret('0A1B2C3D'),
    AUTH_JWT_SIGNER_0A1B2C3D: '1700000000',
    ...overrides,
  }
}

test('Product Control team management requires its own explicit internal flag', () => {
  for (const value of [undefined, '', 'true', '0']) assert.equal(runtimeConfig(env({ AUTH_PRODUCT_CONTROL_TEAM_ENABLED: value })).productControlTeamEnabled, false)
  const config = runtimeConfig(env({ AUTH_PRODUCT_CONTROL_TEAM_ENABLED: '1' }))
  assert.equal(config.productControlTeamEnabled, true)
  assert.equal(config.gotrueEnv.AUTH_PRODUCT_CONTROL_TEAM_ENABLED, undefined)
})

test('builds the GoTrue environment from sealed settings', () => {
  const config = runtimeConfig(env({ AUTH_REDIRECT_URLS: 'https://shop.example.com/callback,http://localhost:3000/cb', AUTH_AUTOCONFIRM: '1' }))
  const g = config.gotrueEnv
  assert.equal(g.API_EXTERNAL_URL, 'https://shop-auth.gregale.app/auth/v1')
  assert.equal(g.GOTRUE_JWT_ISSUER, 'https://shop-auth.gregale.app/auth/v1')
  assert.equal(g.GOTRUE_API_HOST, '127.0.0.1')
  assert.equal(g.GOTRUE_JWT_EXP, '600')
  assert.equal(g.GOTRUE_DB_MAX_POOL_SIZE, '4')
  assert.equal(g.DB_NAMESPACE, 'auth')
  assert.equal(g.GOTRUE_MAILER_AUTOCONFIRM, 'true')
  assert.equal(g.GOTRUE_DISABLE_SIGNUP, 'false')
  assert.equal(g.GOTRUE_RATE_LIMIT_HEADER, 'X-Real-IP')
  assert.equal(g.GOTRUE_LOG_LEVEL, 'warn')
  assert.equal(g.GOTRUE_URI_ALLOW_LIST, 'https://shop.example.com/callback,http://localhost:3000/cb,https://shop-auth.gregale.app/ui/**')
  assert.equal(new URL(g.DATABASE_URL).searchParams.get('sslmode'), 'verify-full')
  // The child gets an explicit environment, never the whole process env.
  assert.equal(g.AUTH_JWT_SECRET, undefined)
  assert.equal(g.PATH, undefined)
})

test('passes provider settings through but rejects platform-owned GoTrue keys', () => {
  const config = runtimeConfig(env({ GOTRUE_EXTERNAL_GITHUB_ENABLED: 'true', GOTRUE_EXTERNAL_GITHUB_CLIENT_ID: 'id', GOTRUE_MFA_TOTP_ENROLL_ENABLED: 'true', GOTRUE_PASSWORD_MIN_LENGTH: '12' }))
  assert.equal(config.gotrueEnv.GOTRUE_EXTERNAL_GITHUB_CLIENT_ID, 'id')
  assert.equal(config.gotrueEnv.GOTRUE_PASSWORD_MIN_LENGTH, '12')
  for (const managed of ['GOTRUE_JWT_SECRET', 'GOTRUE_JWT_EXP', 'GOTRUE_DB_NAMESPACE', 'GOTRUE_HOOK_SEND_EMAIL_URI', 'GOTRUE_SMTP_HOST', 'GOTRUE_LOG_LEVEL']) {
    assert.throws(() => runtimeConfig(env({ [managed]: 'override' })), /managed by Gregale/, managed)
  }
})

test('rejects unsafe URLs and secrets', () => {
  for (const [name, value] of [
    ['AUTH_EXTERNAL_URL', 'http://shop-auth.gregale.app'],
    ['AUTH_EXTERNAL_URL', 'https://shop-auth.gregale.app/path'],
    ['AUTH_EXTERNAL_URL', 'https://user:pass@shop-auth.gregale.app'],
    ['AUTH_SITE_URL', 'http://shop.example.com'],
    ['AUTH_SITE_URL', 'javascript:alert(1)'],
    ['AUTH_REDIRECT_URLS', 'https://ok.example.com,ftp://bad.example.com'],
    ['AUTH_JWT_SECRET', 'short'],
    ['AUTH_AUTOCONFIRM', 'yes'],
  ]) {
    assert.throws(() => runtimeConfig(env({ [name]: value })), undefined, `${name}=${value}`)
  }
  assert.throws(() => runtimeConfig(env({ AUTH_REDIRECT_URLS: Array.from({ length: 51 }, (_, i) => `https://e${i}.example.com`).join(',') })))
  assert.throws(() => databaseURL('postgresql://u:p@db.example.com/app?sslmode=disable'), /verified TLS/)
  assert.match(databaseURL('postgresql://u:p@127.0.0.1/app?sslmode=disable'), /sslmode=disable/)
})

test('one signing key and up to three verification-only keys, each its own secret', () => {
  const keys = signingKeys({ ...keySecret('0A1B2C3D'), ...keySecret('FFFF0001'), AUTH_JWT_SIGNER_0A1B2C3D: '1700000000', AUTH_JWT_SIGNER_FFFF0001: '1700000600', OTHER: 'x' })
  assert.equal(keys.signing, 'FFFF0001', 'the newest signer marker wins')
  assert.deepEqual(keys.verifying, ['0A1B2C3D'])
  const gotrue = JSON.parse(keys.gotrue)
  assert.deepEqual(gotrue.map(k => [k.kid, k.key_ops]), [['gregale-0a1b2c3d', ['verify']], ['gregale-ffff0001', ['sign', 'verify']]])
  assert.ok(gotrue.every(k => k.alg === 'ES256' && typeof k.d === 'string'))
  const four = { ...keySecret('00000001'), ...keySecret('00000002'), ...keySecret('00000003'), ...keySecret('00000004'), AUTH_JWT_SIGNER_00000001: '1700000000' }
  assert.doesNotThrow(() => signingKeys(four))
  assert.throws(() => signingKeys({ ...four, ...keySecret('00000005') }), /one to 4/)
  assert.throws(() => signingKeys({ AUTH_JWT_SIGNER_00000001: '1700000000' }), /one to 4/)
  assert.throws(() => signingKeys({ ...keySecret('0A1B2C3D') }), /Exactly one newest/)
  assert.throws(() => signingKeys({ ...keySecret('0A1B2C3D'), ...keySecret('FFFF0001'), AUTH_JWT_SIGNER_0A1B2C3D: '1700000000', AUTH_JWT_SIGNER_FFFF0001: '1700000000' }), /Exactly one newest/)
  assert.throws(() => signingKeys({ ...keySecret('0A1B2C3D'), AUTH_JWT_SIGNER_0A1B2C3D: 'now' }), /Unix time/)
  assert.throws(() => signingKeys({ ...keySecret('0A1B2C3D'), AUTH_JWT_SIGNER_FFFF0001: '1700000000' }), /names no AUTH_JWT_KEY_FFFF0001/)
  const jwk = JSON.parse(keySecret('0A1B2C3D').AUTH_JWT_KEY_0A1B2C3D)
  const { d, ...publicOnly } = jwk
  assert.ok(d)
  for (const bad of [publicOnly, { ...jwk, kid: 'gregale-ffff0001' }, { ...jwk, alg: 'RS256' }, { ...jwk, d: 'AAAA' }]) {
    assert.throws(() => signingKeys({ AUTH_JWT_KEY_0A1B2C3D: JSON.stringify(bad), AUTH_JWT_SIGNER_0A1B2C3D: '1700000000' }), /ES256 private key gregale-0a1b2c3d/)
  }
  assert.throws(() => signingKeys({ AUTH_JWT_KEY_0A1B2C3D: 'not json', AUTH_JWT_SIGNER_0A1B2C3D: '1700000000' }), /Invalid AUTH_JWT_KEY_0A1B2C3D/)
})

test('enabled providers get this app\'s callback, and settings report only public values', () => {
  const config = runtimeConfig(env({
    ...keySecret('FFFF0001'),
    GOTRUE_EXTERNAL_GOOGLE_ENABLED: 'true', GOTRUE_EXTERNAL_GOOGLE_CLIENT_ID: 'id', GOTRUE_EXTERNAL_GOOGLE_SECRET: 'provider-secret',
    GOTRUE_EXTERNAL_GITHUB_ENABLED: 'true', GOTRUE_EXTERNAL_GITHUB_REDIRECT_URI: 'https://custom.example.com/cb',
    GOTRUE_EXTERNAL_DISCORD_ENABLED: 'false',
    GOTRUE_PASSWORD_MIN_LENGTH: '12', GOTRUE_PASSWORD_REQUIRED_CHARACTERS: [characterClasses.lower, characterClasses.digit, 'xyz'].join(':'),
    GOTRUE_MFA_TOTP_ENROLL_ENABLED: 'false', AUTH_DISABLE_SIGNUP: '1',
  }))
  assert.equal(config.gotrueEnv.GOTRUE_EXTERNAL_GOOGLE_REDIRECT_URI, 'https://shop-auth.gregale.app/auth/v1/callback')
  assert.equal(config.gotrueEnv.GOTRUE_EXTERNAL_GITHUB_REDIRECT_URI, 'https://custom.example.com/cb')
  assert.equal(config.gotrueEnv.GOTRUE_EXTERNAL_DISCORD_REDIRECT_URI, undefined)
  assert.deepEqual(config.settings, {
    app: 'shop.example.com', site_url: 'https://shop.example.com', redirect_urls: [],
    signup: { enabled: false, autoconfirm: false }, providers: ['github', 'google'],
    password: { min_length: 12, required: ['lower', 'digit', 'custom'], leaked_password_check: false },
    mfa: { totp: false }, email: { configured: false }, keys: { signing: '0A1B2C3D', verifying: ['FFFF0001'] },
  })
  assert.ok(!JSON.stringify(config.settings).includes('provider-secret'))
  assert.deepEqual(runtimeConfig(env()).settings.password, { min_length: 6, required: [], leaked_password_check: false })
  assert.equal(runtimeConfig(env()).settings.mfa.totp, true)
})

test('migrations lift the statement timeout for the release task only', () => {
  const config = runtimeConfig(env())
  assert.equal(new URL(migrationEnv(config).DATABASE_URL).searchParams.get('statement_timeout'), '600000')
  assert.equal(new URL(config.gotrueEnv.DATABASE_URL).searchParams.get('statement_timeout'), null)
})

test('accepts a declarative AUTH_AUTHZ object and derives browser origins', () => {
  const config = runtimeConfig(env({ AUTH_REDIRECT_URLS: 'https://shop.example.com/cb,http://localhost:3000/cb', AUTH_AUTHZ: '{"roles":{"editor":["doc:write"]}}' }))
  assert.deepEqual(JSON.parse(config.authz), { roles: { editor: ['doc:write'] } })
  assert.deepEqual(config.origins, ['https://shop.example.com', 'http://localhost:3000'])
  assert.equal(authzConfig(undefined), '{}')
  for (const bad of ['[]', 'null', '"x"', 'not json', JSON.stringify({ roles: { r: ['x'.repeat(70000)] } })]) {
    assert.throws(() => authzConfig(bad), undefined, bad.slice(0, 20))
  }
})
