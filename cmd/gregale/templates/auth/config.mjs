// Mirrors pkg/api/limits.go; TestAuthTemplateRuntimeBounds checks this contract.
export const limits = Object.freeze({ accessTokenSeconds: 600, pool: 4, frontPool: 2, bodyBytes: 1048576, authzBodyBytes: 65536, authzConfigBytes: 65536, redirectURLs: 50, migrationStatementMs: 600000, invitationSeconds: 604800, emailTimeoutMs: 10000, hookBodyBytes: 262144, storageURLSeconds: 60, adminBodyBytes: 1048576, importBatch: 100, signingKeys: 4, teamMembers: 500, teamInvitations: 500, teamEmailBytes: 320, teamContextBytes: 4096, teamInvitationMinSeconds: 300, teamInvitationMaxSeconds: 1209600, teamHistoryPageSize: 100, teamCursorBytes: 1024, emailGrantSeconds: 2592000 })

import { createPrivateKey } from 'node:crypto'

export const gotrue = Object.freeze({ binary: '/usr/local/bin/gotrue-auth', migrations: '/app/gotrue-migrations', host: '127.0.0.1', port: 9999 })

// Customers configure sign-in providers, MFA, password rules and CAPTCHA by
// setting these GoTrue variables as app secrets. Everything else is owned by
// Gregale; any other GOTRUE_ variable fails startup instead of being ignored.
const passthroughPrefixes = ['GOTRUE_EXTERNAL_', 'GOTRUE_MFA_', 'GOTRUE_PASSWORD_', 'GOTRUE_SECURITY_CAPTCHA_']

// OAuth providers `gregale auth providers` configures; GoTrue v2.197.0 names.
// Mirrored by cmd/gregale (TestAuthTemplateSettingsContract).
export const providers = Object.freeze(['apple', 'azure', 'bitbucket', 'discord', 'facebook', 'figma', 'github', 'gitlab', 'google', 'kakao', 'keycloak', 'linkedin_oidc', 'notion', 'slack_oidc', 'spotify', 'twitch', 'workos', 'x', 'zoom'])

// Password character classes for GOTRUE_PASSWORD_REQUIRED_CHARACTERS, which
// separates classes with ':'. No class contains ':' or a backslash, so none needs
// escaping. Mirrored by cmd/gregale (TestAuthTemplateSettingsContract).
export const characterClasses = Object.freeze({
  lower: 'abcdefghijklmnopqrstuvwxyz',
  upper: 'ABCDEFGHIJKLMNOPQRSTUVWXYZ',
  digit: '0123456789',
  symbol: '!@#$%^&*()_+-=[]{};\'"|<>?,./`~',
})

export function databaseURL(value) {
  const url = new URL(value)
  if (!['postgres:', 'postgresql:'].includes(url.protocol) || !url.username || !url.pathname.slice(1)) throw new Error('Invalid DATABASE_URL')
  const local = ['localhost', '127.0.0.1', '[::1]'].includes(url.hostname)
  if (!local && url.searchParams.get('sslmode') === 'disable') throw new Error('Remote PostgreSQL requires verified TLS')
  if (!local) url.searchParams.set('sslmode', 'verify-full')
  return url.toString()
}

function httpsOrigin(value, name) {
  let url
  try { url = new URL(value) } catch { throw new Error(`Invalid ${name}`) }
  if (url.protocol !== 'https:' || url.username || url.password || url.search || url.hash || (url.pathname !== '/' && url.pathname !== '')) throw new Error(`${name} must be an HTTPS origin`)
  return url.origin
}

// Redirect targets may be local development servers; everything else is HTTPS.
function redirectURL(value, name) {
  let url
  try { url = new URL(value) } catch { throw new Error(`Invalid ${name}`) }
  const loopback = ['localhost', '127.0.0.1', '[::1]'].includes(url.hostname)
  if (!(url.protocol === 'https:' || (url.protocol === 'http:' && loopback)) || url.username || url.password || url.hash) throw new Error(`${name} must be HTTPS or a loopback HTTP URL`)
  return value
}

const signingKeyName = /^AUTH_JWT_KEY_([0-9A-F]{8})$/
const signerName = /^AUTH_JWT_SIGNER_([0-9A-F]{8})$/

export const signingKeyID = id => `gregale-${id.toLowerCase()}`

// Each signing key is its own sealed app secret, AUTH_JWT_KEY_<ID>, holding
// one private ES256 JWK. AUTH_JWT_SIGNER_<ID> secrets hold the Unix time a key
// was made the signer, and the newest one signs, so activating a key is one
// write and the CLI can see from secret names which keys are or are about to
// become the signer. The other keys only verify: a standby key is published
// before it signs, and a previous key keeps its unexpired tokens valid.
// Rotating therefore never reads a private key back out of a write-only
// secret. The result feeds GOTRUE_JWT_KEYS, in which exactly one key signs.
export function signingKeys(env) {
  const keys = []
  for (const [name, value] of Object.entries(env)) {
    const id = signingKeyName.exec(name)?.[1]
    if (!id) continue
    let jwk
    try { jwk = JSON.parse(value) } catch { throw new Error(`Invalid ${name}`) }
    const valid = jwk && typeof jwk === 'object' && jwk.kty === 'EC' && jwk.crv === 'P-256' && jwk.alg === 'ES256' && jwk.kid === signingKeyID(id) && ['x', 'y', 'd'].every(field => typeof jwk[field] === 'string' && /^[A-Za-z0-9_-]{43}$/.test(jwk[field]) && Buffer.from(jwk[field], 'base64url').toString('base64url') === jwk[field])
    if (!valid) throw new Error(`${name} must hold the ES256 private key ${signingKeyID(id)}`)
    try { createPrivateKey({ key: { kty: jwk.kty, crv: jwk.crv, x: jwk.x, y: jwk.y, d: jwk.d }, format: 'jwk' }) } catch { throw new Error(`${name} must hold the ES256 private key ${signingKeyID(id)}`) }
    keys.push({ id, jwk })
  }
  if (keys.length < 1 || keys.length > limits.signingKeys) throw new Error(`Configure one to ${limits.signingKeys} AUTH_JWT_KEY_<ID> secrets`)
  const signers = []
  for (const [name, value] of Object.entries(env)) {
    const id = signerName.exec(name)?.[1]
    if (!id) continue
    if (!/^[1-9][0-9]{0,11}$/.test(value)) throw new Error(`${name} must hold a Unix time`)
    signers.push({ id, at: Number(value) })
  }
  signers.sort((a, b) => b.at - a.at)
  if (signers.length === 0 || (signers.length > 1 && signers[0].at === signers[1].at)) throw new Error('Exactly one newest AUTH_JWT_SIGNER_<ID> must name the signing key')
  const signing = signers[0].id
  if (!keys.some(key => key.id === signing)) throw new Error(`AUTH_JWT_SIGNER_${signing} names no AUTH_JWT_KEY_${signing}`)
  keys.sort((a, b) => a.id.localeCompare(b.id))
  return {
    signing,
    verifying: keys.map(key => key.id).filter(id => id !== signing),
    gotrue: JSON.stringify(keys.map(({ id, jwk }) => ({ kty: 'EC', crv: 'P-256', alg: 'ES256', kid: jwk.kid, x: jwk.x, y: jwk.y, d: jwk.d, key_ops: id === signing ? ['sign', 'verify'] : ['verify'] }))),
  }
}

export function runtimeConfig(env) {
  const external = httpsOrigin(env.AUTH_EXTERNAL_URL, 'AUTH_EXTERNAL_URL')
  const site = redirectURL(env.AUTH_SITE_URL, 'AUTH_SITE_URL')
  const redirects = (env.AUTH_REDIRECT_URLS ?? '').split(',').filter(Boolean)
  if (redirects.length > limits.redirectURLs) throw new Error('Too many AUTH_REDIRECT_URLS')
  redirects.forEach(value => redirectURL(value, 'AUTH_REDIRECT_URLS'))
  if (typeof env.AUTH_JWT_SECRET !== 'string' || env.AUTH_JWT_SECRET.length < 32) throw new Error('AUTH_JWT_SECRET must hold at least 32 characters')
  for (const flag of ['AUTH_AUTOCONFIRM', 'AUTH_DISABLE_SIGNUP']) {
    if (env[flag] !== undefined && env[flag] !== '1' && env[flag] !== '0') throw new Error(`Invalid ${flag}`)
  }
  const passthrough = {}
  for (const [key, value] of Object.entries(env)) {
    if (!key.startsWith('GOTRUE_')) continue
    if (!passthroughPrefixes.some(prefix => key.startsWith(prefix))) throw new Error(`${key} is managed by Gregale; remove the app secret`)
    passthrough[key] = value
  }
  const api = `${external}/auth/v1`
  const keys = signingKeys(env)
  // GoTrue needs each provider's callback; it is always this app's.
  const callbacks = {}
  for (const provider of providers) {
    const prefix = `GOTRUE_EXTERNAL_${provider.toUpperCase()}`
    if (passthrough[`${prefix}_ENABLED`] === 'true' && !passthrough[`${prefix}_REDIRECT_URI`]) callbacks[`${prefix}_REDIRECT_URI`] = `${api}/callback`
  }
  const gotrueEnv = {
    ...callbacks,
    ...passthrough,
    GOTRUE_API_HOST: gotrue.host,
    PORT: String(gotrue.port),
    API_EXTERNAL_URL: api,
    GOTRUE_SITE_URL: site,
    // The hosted pages are always a valid redirect target (password reset).
    GOTRUE_URI_ALLOW_LIST: [...redirects, `${external}/ui/**`].join(','),
    GOTRUE_DB_DRIVER: 'postgres',
    DATABASE_URL: databaseURL(env.DATABASE_URL),
    DB_NAMESPACE: 'auth',
    GOTRUE_DB_MAX_POOL_SIZE: String(limits.pool),
    GOTRUE_JWT_SECRET: env.AUTH_JWT_SECRET,
    GOTRUE_JWT_KEYS: keys.gotrue,
    GOTRUE_JWT_EXP: String(limits.accessTokenSeconds),
    GOTRUE_JWT_AUD: 'authenticated',
    GOTRUE_JWT_ISSUER: api,
    GOTRUE_JWT_ADMIN_ROLES: 'service_role',
    GOTRUE_EXTERNAL_EMAIL_ENABLED: passthrough.GOTRUE_EXTERNAL_EMAIL_ENABLED ?? 'true',
    GOTRUE_MAILER_AUTOCONFIRM: env.AUTH_AUTOCONFIRM === '1' ? 'true' : 'false',
    GOTRUE_DISABLE_SIGNUP: env.AUTH_DISABLE_SIGNUP === '1' ? 'true' : 'false',
    GOTRUE_SECURITY_REFRESH_TOKEN_ROTATION_ENABLED: 'true',
    // The public gateway replaces X-Real-IP with the verified client address.
    GOTRUE_RATE_LIMIT_HEADER: 'X-Real-IP',
    // GoTrue v2.197.0 does not apply this to its request and audit logs,
    // which carry user email addresses; logs.mjs filters them as well.
    GOTRUE_LOG_LEVEL: 'warn',
  }
  const email = emailConfig(env)
  const config = { external, api, gotrueEnv, redirects, keys: { signing: keys.signing, verifying: keys.verifying }, authz: authzConfig(env.AUTH_AUTHZ), origins: browserOrigins(site, redirects), email, app: new URL(site).host, productControlTeamEnabled: env.AUTH_PRODUCT_CONTROL_TEAM_ENABLED === '1', productControlEmailEnabled: env.AUTH_PRODUCT_CONTROL_EMAIL_ENABLED === '1' }
  return { ...config, settings: publicSettings(config) }
}

// publicSettings is what /settings/v1 reports: only values that are public
// anyway (published key IDs, enabled providers, rules a sign-up form shows)
// or harmless. The CLI and dashboard read settings here because app secrets
// are write-only.
export function publicSettings({ gotrueEnv: g, redirects, keys, email, app }) {
  const flag = (value, fallback) => value === undefined ? fallback : value === 'true'
  const required = (g.GOTRUE_PASSWORD_REQUIRED_CHARACTERS ?? '').split(':').filter(Boolean)
    .map(chars => Object.keys(characterClasses).find(name => characterClasses[name] === chars) ?? 'custom')
  return {
    app,
    site_url: g.GOTRUE_SITE_URL,
    redirect_urls: redirects,
    signup: { enabled: g.GOTRUE_DISABLE_SIGNUP !== 'true', autoconfirm: g.GOTRUE_MAILER_AUTOCONFIRM === 'true' },
    providers: providers.filter(provider => g[`GOTRUE_EXTERNAL_${provider.toUpperCase()}_ENABLED`] === 'true'),
    // GoTrue's defaults: six characters, no classes, TOTP enrolment on.
    // `--mfa off` stops enrolment only; enrolled users keep verifying.
    password: { min_length: Number(g.GOTRUE_PASSWORD_MIN_LENGTH ?? 6), required, leaked_password_check: flag(g.GOTRUE_PASSWORD_HIBP_ENABLED, false) },
    mfa: { totp: flag(g.GOTRUE_MFA_TOTP_ENROLL_ENABLED, true) },
    email: { configured: email !== null },
    keys,
  }
}

// AUTH_AUTHZ declares custom roles and policies; gregale_auth.apply_authz
// validates the semantics in the release task. Unset means built-in roles only.
export function authzConfig(value) {
  if (value === undefined || value === '') return '{}'
  if (Buffer.byteLength(value) > limits.authzConfigBytes) throw new Error('AUTH_AUTHZ is too large')
  let parsed
  try { parsed = JSON.parse(value) } catch { throw new Error('Invalid AUTH_AUTHZ') }
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) throw new Error('AUTH_AUTHZ must be a JSON object')
  return JSON.stringify(parsed)
}

// AUTH_EMAIL_* select the HTTPS email provider. Without them, flows that send
// email fail with email_provider_unconfigured instead of trying SMTP.
export function emailConfig(env) {
  const provider = env.AUTH_EMAIL_PROVIDER ?? ''
  if (provider === '') {
    if (env.AUTH_EMAIL_API_KEY || env.AUTH_EMAIL_FROM) throw new Error('AUTH_EMAIL_PROVIDER is required with AUTH_EMAIL_API_KEY or AUTH_EMAIL_FROM')
    return null
  }
  if (!['resend', 'postmark'].includes(provider)) throw new Error('AUTH_EMAIL_PROVIDER must be resend or postmark')
  if (typeof env.AUTH_EMAIL_API_KEY !== 'string' || env.AUTH_EMAIL_API_KEY.length < 8 || /\s/.test(env.AUTH_EMAIL_API_KEY)) throw new Error('AUTH_EMAIL_API_KEY is required')
  const from = env.AUTH_EMAIL_FROM ?? ''
  if (!/^([^<>\r\n]{1,100} <[^@\s<>]+@[^@\s<>]+\.[^@\s<>]+>|[^@\s<>]+@[^@\s<>]+\.[^@\s<>]+)$/.test(from)) throw new Error('AUTH_EMAIL_FROM must be an address or "Name <address>"')
  return { provider, apiKey: env.AUTH_EMAIL_API_KEY, from }
}

// Browsers call /authz/v1 from the application's own origins.
function browserOrigins(site, redirects) {
  return [...new Set([site, ...redirects].map(value => new URL(value).origin))]
}

// Migrations run in the release task and may legitimately exceed the auth
// login's default statement timeout on large user tables.
export function migrationEnv(config) {
  const url = new URL(config.gotrueEnv.DATABASE_URL)
  url.searchParams.set('statement_timeout', String(limits.migrationStatementMs))
  return { ...config.gotrueEnv, DATABASE_URL: url.toString() }
}
