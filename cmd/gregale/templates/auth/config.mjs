// Mirrors pkg/api/limits.go; TestAuthTemplateRuntimeBounds checks this contract.
export const limits = Object.freeze({ accessTokenSeconds: 600, pool: 4, frontPool: 2, bodyBytes: 1048576, authzBodyBytes: 65536, authzConfigBytes: 65536, redirectURLs: 50, migrationStatementMs: 600000, invitationSeconds: 604800 })

export const gotrue = Object.freeze({ binary: '/usr/local/bin/gotrue-auth', migrations: '/app/gotrue-migrations', host: '127.0.0.1', port: 9999 })

// Customers configure sign-in providers, MFA, password rules and CAPTCHA by
// setting these GoTrue variables as app secrets. Everything else is owned by
// Gregale; any other GOTRUE_ variable fails startup instead of being ignored.
const passthroughPrefixes = ['GOTRUE_EXTERNAL_', 'GOTRUE_MFA_', 'GOTRUE_PASSWORD_', 'GOTRUE_SECURITY_CAPTCHA_']

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

// GOTRUE_JWT_KEYS is a JSON array of private JWKs; exactly one may sign.
// Verification-only keys keep previously issued tokens valid during rotation.
export function signingKeys(value) {
  let keys
  try { keys = JSON.parse(value) } catch { throw new Error('Invalid AUTH_JWT_KEYS') }
  if (!Array.isArray(keys) || keys.length < 1 || keys.length > 4) throw new Error('AUTH_JWT_KEYS must hold one to four keys')
  const kids = new Set()
  let signers = 0
  for (const key of keys) {
    if (!key || typeof key !== 'object' || key.kty !== 'EC' || key.crv !== 'P-256' || key.alg !== 'ES256' || typeof key.d !== 'string' || typeof key.kid !== 'string' || !/^[A-Za-z0-9_-]{8,64}$/.test(key.kid) || kids.has(key.kid)) throw new Error('AUTH_JWT_KEYS must hold distinct ES256 private keys')
    kids.add(key.kid)
    if (!Array.isArray(key.key_ops) || !key.key_ops.every(op => op === 'sign' || op === 'verify')) throw new Error('Invalid AUTH_JWT_KEYS key_ops')
    if (key.key_ops.includes('sign')) signers++
  }
  if (signers !== 1) throw new Error('AUTH_JWT_KEYS must hold exactly one signing key')
  return JSON.stringify(keys)
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
  const gotrueEnv = {
    ...passthrough,
    GOTRUE_API_HOST: gotrue.host,
    PORT: String(gotrue.port),
    API_EXTERNAL_URL: api,
    GOTRUE_SITE_URL: site,
    GOTRUE_URI_ALLOW_LIST: redirects.join(','),
    GOTRUE_DB_DRIVER: 'postgres',
    DATABASE_URL: databaseURL(env.DATABASE_URL),
    DB_NAMESPACE: 'auth',
    GOTRUE_DB_MAX_POOL_SIZE: String(limits.pool),
    GOTRUE_DB_MIGRATIONS_PATH: gotrue.migrations,
    GOTRUE_JWT_SECRET: env.AUTH_JWT_SECRET,
    GOTRUE_JWT_KEYS: signingKeys(env.AUTH_JWT_KEYS),
    GOTRUE_JWT_EXP: String(limits.accessTokenSeconds),
    GOTRUE_JWT_AUD: 'authenticated',
    GOTRUE_JWT_ISSUER: api,
    GOTRUE_JWT_DEFAULT_GROUP_NAME: 'authenticated',
    GOTRUE_JWT_ADMIN_ROLES: 'service_role',
    GOTRUE_EXTERNAL_EMAIL_ENABLED: passthrough.GOTRUE_EXTERNAL_EMAIL_ENABLED ?? 'true',
    GOTRUE_MAILER_AUTOCONFIRM: env.AUTH_AUTOCONFIRM === '1' ? 'true' : 'false',
    GOTRUE_DISABLE_SIGNUP: env.AUTH_DISABLE_SIGNUP === '1' ? 'true' : 'false',
    GOTRUE_SECURITY_REFRESH_TOKEN_ROTATION_ENABLED: 'true',
    // The public gateway replaces X-Real-IP with the verified client address.
    GOTRUE_RATE_LIMIT_HEADER: 'X-Real-IP',
    // Request logs would carry verification tokens from /verify query strings.
    GOTRUE_LOG_LEVEL: 'warn',
  }
  return { external, api, gotrueEnv, authz: authzConfig(env.AUTH_AUTHZ), origins: browserOrigins(site, redirects) }
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
