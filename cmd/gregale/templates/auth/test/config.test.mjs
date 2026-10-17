import test from 'node:test'
import assert from 'node:assert/strict'
import { generateKeyPairSync } from 'node:crypto'
import { authzConfig, characterClasses, databaseURL, goMemoryLimit, migrationEnv, runtimeConfig, signingKeys } from '../config.mjs'

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

test('GoTrue collects garbage within a third of the instance memory', () => {
  assert.equal(goMemoryLimit(136 * 1048576), '45MiB')
  assert.equal(goMemoryLimit(64 * 1048576), '32MiB')
  assert.equal(goMemoryLimit(1032 * 1048576), '344MiB')
  assert.match(goMemoryLimit(), /^\d+MiB$/)
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
  const different = JSON.parse(keySecret('FFFF0001').AUTH_JWT_KEY_FFFF0001)
  for (const bad of [publicOnly, { ...jwk, kid: 'gregale-ffff0001' }, { ...jwk, alg: 'RS256' }, { ...jwk, d: 'AAAA' },
    { ...jwk, d: Buffer.alloc(32).toString('base64url') }, { ...jwk, d: different.d }]) {
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
    mfa: { totp: false, phone: false, recovery_codes: false }, email: { configured: false, provider: null, from: null }, events: { lifecycle: false },
    passkeys: { enabled: false, rp_id: 'shop-auth.gregale.app', origins: ['https://shop-auth.gregale.app'] },
    branding: { name: 'shop.example.com', color: null, logo_url: null }, captcha: null, activity: { retention_days: null }, phone: { enabled: false, provider: null, countries: null }, sso: { enabled: false, entity_id: null, acs_url: null, self_service: false }, oauth: { enabled: false, dynamic_registration: false, issuer: null, metadata_url: null, default_audience: null }, keys: { signing: '0A1B2C3D', verifying: ['FFFF0001'] },
  })
  assert.ok(!JSON.stringify(config.settings).includes('provider-secret'))
  assert.deepEqual(runtimeConfig(env()).settings.password, { min_length: 6, required: [], leaked_password_check: false })
  assert.equal(runtimeConfig(env()).settings.mfa.totp, true)
  const withEmail = runtimeConfig(env({ AUTH_EMAIL_PROVIDER: 'resend', AUTH_EMAIL_FROM: 'Shop <auth@shop.example.com>', AUTH_EMAIL_API_KEY: 're_sealed_key_1' })).settings
  assert.deepEqual(withEmail.email, { configured: true, provider: 'resend', from: 'Shop <auth@shop.example.com>' })
  assert.ok(!JSON.stringify(withEmail).includes('re_sealed_key_1'))
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

test('passkeys belong to the Auth host unless a shared parent domain is named', () => {
  const own = runtimeConfig(env({ GOTRUE_PASSKEY_ENABLED: 'true', AUTH_REDIRECT_URLS: 'https://shop-auth.gregale.app/after' }))
  assert.equal(own.gotrueEnv.GOTRUE_PASSKEY_ENABLED, 'true')
  assert.equal(own.gotrueEnv.GOTRUE_WEBAUTHN_RP_ID, 'shop-auth.gregale.app')
  assert.equal(own.gotrueEnv.GOTRUE_WEBAUTHN_RP_ORIGINS, 'https://shop-auth.gregale.app')
  assert.equal(own.gotrueEnv.GOTRUE_WEBAUTHN_RP_DISPLAY_NAME, 'shop.example.com')
  assert.equal(own.settings.passkeys.enabled, true)
  const shared = runtimeConfig(env({ AUTH_EXTERNAL_URL: 'https://auth.example.com', AUTH_SITE_URL: 'https://shop.example.com', AUTH_PASSKEY_RP_ID: 'example.com',
    AUTH_REDIRECT_URLS: 'https://admin.example.com/cb,https://other.test/cb,http://localhost:3000/cb', AUTH_UI_NAME: 'Shop' }))
  assert.equal(shared.gotrueEnv.GOTRUE_WEBAUTHN_RP_ID, 'example.com')
  assert.equal(shared.gotrueEnv.GOTRUE_WEBAUTHN_RP_ORIGINS, 'https://auth.example.com,https://shop.example.com,https://admin.example.com')
  assert.equal(shared.gotrueEnv.GOTRUE_WEBAUTHN_RP_DISPLAY_NAME, 'Shop')
  for (const rpID of ['gregale.app.evil.com', 'other.example', 'Example.com', 'com.', '-bad.com']) {
    assert.throws(() => runtimeConfig(env({ AUTH_EXTERNAL_URL: 'https://auth.example.com', AUTH_PASSKEY_RP_ID: rpID })), /AUTH_PASSKEY_RP_ID/, rpID)
  }
  for (const managed of ['GOTRUE_WEBAUTHN_RP_ID', 'GOTRUE_WEBAUTHN_RP_ORIGINS']) {
    assert.throws(() => runtimeConfig(env({ [managed]: 'evil.com' })), /managed by Gregale/, managed)
  }
})

test('branding and CAPTCHA settings are validated', () => {
  const branded = runtimeConfig(env({ AUTH_UI_NAME: 'Shop', AUTH_UI_COLOR: '#4f46e5', AUTH_UI_LOGO_URL: 'https://cdn.example.com/logo.svg' }))
  assert.equal(branded.app, 'Shop')
  assert.deepEqual(branded.settings.branding, { name: 'Shop', color: '#4f46e5', logo_url: 'https://cdn.example.com/logo.svg' })
  assert.equal(branded.gotrueEnv.GOTRUE_WEBAUTHN_RP_DISPLAY_NAME, 'Shop')
  for (const [name, value] of [['AUTH_UI_NAME', 'x'.repeat(61)], ['AUTH_UI_NAME', '<script>'], ['AUTH_UI_COLOR', 'red'], ['AUTH_UI_COLOR', '#4F46E5'], ['AUTH_UI_LOGO_URL', 'http://cdn.example.com/a.png'], ['AUTH_UI_LOGO_URL', 'https://u:p@cdn.example.com/a.png']]) {
    assert.throws(() => runtimeConfig(env({ [name]: value })), undefined, `${name}=${value}`)
  }
  const turnstile = { GOTRUE_SECURITY_CAPTCHA_ENABLED: 'true', GOTRUE_SECURITY_CAPTCHA_PROVIDER: 'turnstile', GOTRUE_SECURITY_CAPTCHA_SECRET: 'secret-key', AUTH_CAPTCHA_SITE_KEY: '1x00000000000000000000AA' }
  const config = runtimeConfig(env(turnstile))
  assert.deepEqual(config.settings.captcha, { provider: 'turnstile', site_key: '1x00000000000000000000AA' })
  assert.equal(config.gotrueEnv.GOTRUE_SECURITY_CAPTCHA_PROVIDER, 'turnstile')
  assert.ok(!JSON.stringify(config.settings).includes('secret-key'))
  for (const broken of [{ GOTRUE_SECURITY_CAPTCHA_PROVIDER: 'recaptcha' }, { AUTH_CAPTCHA_SITE_KEY: '' }, { GOTRUE_SECURITY_CAPTCHA_SECRET: '' }]) {
    assert.throws(() => runtimeConfig(env({ ...turnstile, ...broken })), /CAPTCHA/, JSON.stringify(broken))
  }
})

test('phone sign-in needs a known SMS provider and names the app in the message', () => {
  const twilio = { GOTRUE_EXTERNAL_PHONE_ENABLED: 'true', GOTRUE_SMS_PROVIDER: 'twilio', GOTRUE_SMS_TWILIO_ACCOUNT_SID: 'AC1', GOTRUE_SMS_TWILIO_MESSAGE_SERVICE_SID: 'MG1', GOTRUE_SMS_TWILIO_AUTH_TOKEN: 'token-secret' }
  const config = runtimeConfig(env({ ...twilio, AUTH_UI_NAME: 'Shop {{ .Evil }}' }))
  assert.equal(config.gotrueEnv.GOTRUE_SMS_TWILIO_AUTH_TOKEN, 'token-secret')
  assert.equal(config.gotrueEnv.GOTRUE_SMS_TEMPLATE, 'Your Shop  .Evil  code is {{ .Code }}')
  assert.equal(config.gotrueEnv.GOTRUE_SMS_OTP_EXP, '300')
  assert.equal(runtimeConfig(env({ ...twilio, GOTRUE_SMS_OTP_EXP: '120' })).gotrueEnv.GOTRUE_SMS_OTP_EXP, '120')
  assert.deepEqual(config.settings.phone, { enabled: true, provider: 'twilio', countries: null })
  assert.deepEqual(runtimeConfig(env({ ...twilio, AUTH_SMS_COUNTRIES: '1,44' })).settings.phone.countries, ['1', '44'])
  assert.throws(() => runtimeConfig(env({ ...twilio, AUTH_SMS_COUNTRIES: '+44' })), /AUTH_SMS_COUNTRIES/)
  assert.ok(!JSON.stringify(config.settings).includes('token-secret'))
  assert.equal(runtimeConfig(env({ ...twilio, GOTRUE_SMS_TEMPLATE: '{{ .Code }} is your code' })).gotrueEnv.GOTRUE_SMS_TEMPLATE, '{{ .Code }} is your code')
  assert.throws(() => runtimeConfig(env({ GOTRUE_EXTERNAL_PHONE_ENABLED: 'true', GOTRUE_SMS_PROVIDER: 'sinch' })), /GOTRUE_SMS_PROVIDER must be one of/)
  assert.throws(() => runtimeConfig(env({ GOTRUE_EXTERNAL_PHONE_ENABLED: 'true' })), /GOTRUE_SMS_PROVIDER must be one of/)
  const off = runtimeConfig(env({ GOTRUE_EXTERNAL_PHONE_ENABLED: 'false', GOTRUE_SMS_PROVIDER: 'twilio' }))
  assert.equal(off.gotrueEnv.GOTRUE_SMS_TEMPLATE, undefined)
  assert.equal(off.gotrueEnv.GOTRUE_SMS_OTP_EXP, undefined)
  assert.deepEqual(off.settings.phone, { enabled: false, provider: null, countries: null })
  assert.throws(() => runtimeConfig(env({ GOTRUE_RATE_LIMIT_SMS_SENT: '1000' })), /managed by Gregale/)
})

test('SMS codes as a second factor need an SMS provider', () => {
  const sms = { GOTRUE_SMS_PROVIDER: 'twilio', GOTRUE_SMS_TWILIO_ACCOUNT_SID: 'AC1', GOTRUE_SMS_TWILIO_MESSAGE_SERVICE_SID: 'MG1', GOTRUE_SMS_TWILIO_AUTH_TOKEN: 't' }
  const on = runtimeConfig(env({ ...sms, GOTRUE_MFA_PHONE_ENROLL_ENABLED: 'true', GOTRUE_MFA_PHONE_VERIFY_ENABLED: 'true', GOTRUE_MFA_RECOVERY_CODES_ENROLL_ENABLED: 'true', AUTH_UI_NAME: 'Shop' }))
  assert.deepEqual(on.settings.mfa, { totp: true, phone: true, recovery_codes: true })
  assert.equal(on.gotrueEnv.GOTRUE_MFA_PHONE_TEMPLATE, 'Your Shop verification code is {{ .Code }}')
  const errors = []
  const original = console.error
  console.error = line => errors.push(line)
  try {
    const off = runtimeConfig(env({ GOTRUE_MFA_PHONE_ENROLL_ENABLED: 'true', GOTRUE_MFA_PHONE_VERIFY_ENABLED: 'true' }))
    assert.equal(off.gotrueEnv.GOTRUE_MFA_PHONE_ENROLL_ENABLED, 'false')
    assert.equal(off.gotrueEnv.GOTRUE_MFA_PHONE_VERIFY_ENABLED, 'true')
    assert.equal(off.settings.mfa.phone, false)
  } finally { console.error = original }
  assert.deepEqual(errors.map(line => JSON.parse(line).event), ['phone_mfa_without_sms_provider'])
})
