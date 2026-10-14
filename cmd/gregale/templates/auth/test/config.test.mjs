import test from 'node:test'
import assert from 'node:assert/strict'
import { generateKeyPairSync } from 'node:crypto'
import { authzConfig, databaseURL, migrationEnv, runtimeConfig, signingKeys } from '../config.mjs'

function key(kid, ops = ['sign', 'verify']) {
  const { privateKey } = generateKeyPairSync('ec', { namedCurve: 'P-256' })
  return { ...privateKey.export({ format: 'jwk' }), kid, alg: 'ES256', key_ops: ops }
}

function env(overrides = {}) {
  return {
    DATABASE_URL: 'postgresql://gregale_auth_x:secret@db.example.com:5432/app?sslmode=require',
    AUTH_EXTERNAL_URL: 'https://shop-auth.gregale.app',
    AUTH_SITE_URL: 'https://shop.example.com',
    AUTH_JWT_SECRET: 'x'.repeat(48),
    AUTH_JWT_KEYS: JSON.stringify([key('signing-key-1')]),
    ...overrides,
  }
}

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

test('requires exactly one ES256 signing key', () => {
  assert.doesNotThrow(() => signingKeys(JSON.stringify([key('signing-key-2'), key('previous-key-1', ['verify'])])))
  assert.throws(() => signingKeys(JSON.stringify([key('signing-key-2'), key('signing-key-3')])), /exactly one/)
  assert.throws(() => signingKeys(JSON.stringify([key('verify-only-1', ['verify'])])), /exactly one/)
  assert.throws(() => signingKeys(JSON.stringify([key('dup-key-001'), key('dup-key-001', ['verify'])])), /distinct/)
  const { d, ...publicOnly } = key('public-only-1')
  assert.ok(d)
  assert.throws(() => signingKeys(JSON.stringify([publicOnly])), /private/)
  assert.throws(() => signingKeys(JSON.stringify([{ ...key('rsa-key-0001'), alg: 'RS256' }])), /ES256/)
  assert.throws(() => signingKeys('not json'), /Invalid/)
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
