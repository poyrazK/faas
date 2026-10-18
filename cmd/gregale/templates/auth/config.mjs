// Mirrors pkg/api/limits.go; TestAuthTemplateRuntimeBounds checks this contract.
export const limits = Object.freeze({ accessTokenSeconds: 600, pool: 4, frontPool: 2, bodyBytes: 1048576, authzBodyBytes: 65536, authzConfigBytes: 65536, redirectURLs: 50, migrationStatementMs: 600000, invitationSeconds: 604800, emailTimeoutMs: 10000, hookBodyBytes: 262144, storageURLSeconds: 60, adminBodyBytes: 1048576, importBatch: 100, adminPageSize: 100, reauthenticationSeconds: 600, sessionMinMinutes: 15, sessionMaxMinutes: 525600, emailSubjectChars: 150, emailBodyBytes: 4000, signingKeys: 4, uiNameChars: 60, auditRetentionMaxDays: 3650, auditPruneBatch: 5000, auditPruneBatches: 20, auditPruneIntervalSeconds: 21600, eventBatch: 50, eventLeaseSeconds: 120, eventMaxBackoffSeconds: 3600, eventRetentionSeconds: 259200, smsCodeSeconds: 300, ssoMetadataBytes: 524288, ssoDomains: 50, smsCountries: 50, goTrueMemoryDivisor: 3, goTrueMemoryMinMiB: 32, scimPageSize: 100, scimBodyBytes: 65536, oauthClients: 1000, anonymousRetentionMaxDays: 3650, teamMembers: 500, teamInvitations: 500, teamEmailBytes: 320, teamContextBytes: 4096, teamInvitationMinSeconds: 300, teamInvitationMaxSeconds: 1209600, teamHistoryPageSize: 100, teamCursorBytes: 1024, emailGrantSeconds: 2592000 })

import { createECDH, createPrivateKey } from 'node:crypto'
import { totalmem } from 'node:os'
import { smsCountries } from './sms.mjs'

// GoTrue sends people to SITE_URL + consentPath to approve an OAuth client;
// the front redirects them to the hosted consent page (oauth.mjs). Tokens
// issued to a client that named no resource carry oauthAudience.
export const consentPath = '/.gregale-auth/oauth-consent'
export const oauthAudience = 'oauth-client'

export const gotrue = Object.freeze({ binary: '/usr/local/bin/gotrue-auth', migrations: '/app/gotrue-migrations', host: '127.0.0.1', port: 9999 })

// goMemoryLimit is GoTrue's GOMEMLIMIT: a third of the instance's memory, at
// least 32 MiB. GoTrue hashes each recovery code with argon2id (19 MiB), and
// unbounded its heap grew by about 60 MiB while checking one, more than a
// Free instance can spare beside Node.
export function goMemoryLimit(bytes = instanceMemory()) {
  return `${Math.max(limits.goTrueMemoryMinMiB, Math.floor(bytes / 1048576 / limits.goTrueMemoryDivisor))}MiB`
}

// instanceMemory is the smaller of the cgroup limit and physical memory.
function instanceMemory() {
  const constrained = process.constrainedMemory?.() ?? 0
  return constrained > 0 ? Math.min(constrained, totalmem()) : totalmem()
}

// Customers configure sign-in providers, MFA, password rules, CAPTCHA and
// SMS by setting these GoTrue variables as app secrets. Everything else is
// owned by Gregale; any other GOTRUE_ variable fails startup instead of being
// ignored.
const passthroughPrefixes = ['GOTRUE_EXTERNAL_', 'GOTRUE_MFA_', 'GOTRUE_PASSWORD_', 'GOTRUE_SECURITY_CAPTCHA_', 'GOTRUE_PASSKEY_', 'GOTRUE_SMS_']

// SMS services GoTrue v2.197.0 sends phone sign-in codes through, with the
// customer's own account. Mirrored by cmd/gregale
// (TestAuthTemplateSettingsContract).
export const smsProviders = Object.freeze(['twilio', 'twilio_verify', 'messagebird', 'textlocal', 'vonage'])

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
    try {
      createPrivateKey({ key: { kty: jwk.kty, crv: jwk.crv, x: jwk.x, y: jwk.y, d: jwk.d }, format: 'jwk' })
      // OpenSSL may import a short scalar or inconsistent public coordinates.
      // Derive the public point to prove this is one usable P-256 key pair.
      const curve = createECDH('prime256v1')
      curve.setPrivateKey(Buffer.from(jwk.d, 'base64url'))
      const expected = Buffer.concat([Buffer.from([4]), Buffer.from(jwk.x, 'base64url'), Buffer.from(jwk.y, 'base64url')])
      if (!curve.getPublicKey().equals(expected)) throw new Error('inconsistent key pair')
    } catch { throw new Error(`${name} must hold the ES256 private key ${signingKeyID(id)}`) }
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

const hostname = /^(?=.{1,253}$)([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$/

// webauthnEnv derives GoTrue's WebAuthn relying party. Passkeys belong to the
// Auth app's host unless AUTH_PASSKEY_RP_ID names a parent domain that the
// Auth app shares with the site (auth.example.com and shop.example.com share
// example.com), so the hosted pages and the site use the same passkeys. Only
// HTTPS origins on that domain may use them, or HTTP on localhost during
// development. Gregale owns these settings.
export function webauthnEnv(env, external, site, redirects) {
  const host = new URL(external).hostname
  const rpID = env.AUTH_PASSKEY_RP_ID || host
  if (!hostname.test(rpID) || !rpID.includes('.') && rpID !== 'localhost') throw new Error('AUTH_PASSKEY_RP_ID must be a domain name')
  const within = name => name === rpID || name.endsWith('.' + rpID)
  if (!within(host)) throw new Error(`AUTH_PASSKEY_RP_ID must be ${host} or a parent domain of it`)
  const origins = [...new Set([external, site, ...redirects].map(value => new URL(value)))]
    .filter(url => within(url.hostname) && (url.protocol === 'https:' || url.hostname === 'localhost'))
    .map(url => url.origin)
  return {
    GOTRUE_WEBAUTHN_RP_ID: rpID,
    GOTRUE_WEBAUTHN_RP_ORIGINS: [...new Set(origins)].join(','),
    GOTRUE_WEBAUTHN_RP_DISPLAY_NAME: brandingConfig(env, site).name,
  }
}

// The hosted pages' branding: a display name, an accent colour and a logo
// loaded from an HTTPS URL, whose exact origin the pages' policy allows.
export function brandingConfig(env, site) {
  const name = env.AUTH_UI_NAME ?? ''
  if (name.length > limits.uiNameChars || /[\u0000-\u001f\u007f<>]/.test(name)) throw new Error(`AUTH_UI_NAME must be 1 to ${limits.uiNameChars} characters without control characters or angle brackets`)
  const color = env.AUTH_UI_COLOR ?? ''
  if (color && !/^#[0-9a-f]{6}$/.test(color)) throw new Error('AUTH_UI_COLOR must be a lowercase #rrggbb colour')
  let logo = null
  if (env.AUTH_UI_LOGO_URL) {
    try { logo = new URL(env.AUTH_UI_LOGO_URL) } catch { throw new Error('Invalid AUTH_UI_LOGO_URL') }
    if (logo.protocol !== 'https:' || logo.username || logo.password || env.AUTH_UI_LOGO_URL.length > 2048) throw new Error('AUTH_UI_LOGO_URL must be an HTTPS URL without credentials')
  }
  return { name: name || new URL(site).host, color: color || null, logoURL: logo?.toString() ?? null, logoOrigin: logo?.origin ?? null }
}

// GoTrue verifies CAPTCHA tokens with the provider; the hosted pages render
// the provider's widget, which needs its script and frame origins allowed.
export const captchaProviders = Object.freeze({
  turnstile: { script: 'https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit', sources: ['https://challenges.cloudflare.com'] },
  hcaptcha: { script: 'https://js.hcaptcha.com/1/api.js?render=explicit&recaptchacompat=off', sources: ['https://hcaptcha.com', 'https://*.hcaptcha.com'] },
})

export function captchaConfig(env) {
  if (env.GOTRUE_SECURITY_CAPTCHA_ENABLED !== 'true') return null
  const provider = env.GOTRUE_SECURITY_CAPTCHA_PROVIDER
  if (!captchaProviders[provider]) throw new Error('GOTRUE_SECURITY_CAPTCHA_PROVIDER must be turnstile or hcaptcha')
  if (!env.GOTRUE_SECURITY_CAPTCHA_SECRET) throw new Error('GOTRUE_SECURITY_CAPTCHA_SECRET is required with CAPTCHA')
  if (!/^[A-Za-z0-9_-]{8,100}$/.test(env.AUTH_CAPTCHA_SITE_KEY ?? '')) throw new Error('AUTH_CAPTCHA_SITE_KEY is required with CAPTCHA')
  return { provider, siteKey: env.AUTH_CAPTCHA_SITE_KEY, script: captchaProviders[provider].script, sources: captchaProviders[provider].sources }
}

// pagePolicy is the hosted pages' content security policy: only this origin,
// plus the logo's origin and the CAPTCHA provider when configured.
export function pagePolicy(branding, captcha) {
  const extra = captcha?.sources ?? []
  // data: carries the QR code GoTrue returns when an authenticator app is added.
  const img = ["'self'", 'data:', ...(branding.logoOrigin ? [branding.logoOrigin] : [])]
  return ["default-src 'none'", `script-src 'self' ${extra.join(' ')}`.trim(), `style-src 'self' ${extra.join(' ')}`.trim(), `connect-src 'self' ${extra.join(' ')}`.trim(),
    `img-src ${img.join(' ')}`, ...(extra.length ? [`frame-src ${extra.join(' ')}`] : []), "form-action 'self'", "frame-ancestors 'none'", "base-uri 'none'"].join('; ')
}

export function runtimeConfig(env) {
  const external = httpsOrigin(env.AUTH_EXTERNAL_URL, 'AUTH_EXTERNAL_URL')
  const site = redirectURL(env.AUTH_SITE_URL, 'AUTH_SITE_URL')
  const redirects = (env.AUTH_REDIRECT_URLS ?? '').split(',').filter(Boolean)
  if (redirects.length > limits.redirectURLs) throw new Error('Too many AUTH_REDIRECT_URLS')
  redirects.forEach(value => redirectURL(value, 'AUTH_REDIRECT_URLS'))
  if (typeof env.AUTH_JWT_SECRET !== 'string' || env.AUTH_JWT_SECRET.length < 32) throw new Error('AUTH_JWT_SECRET must hold at least 32 characters')
  for (const flag of ['AUTH_AUTOCONFIRM', 'AUTH_DISABLE_SIGNUP', 'AUTH_LIFECYCLE_EVENTS', 'AUTH_SSO', 'AUTH_ORG_SSO', 'AUTH_OAUTH_SERVER', 'AUTH_OAUTH_REGISTRATION', 'AUTH_IDENTITY_LINKING', 'AUTH_SELF_DELETION', 'AUTH_ORG_CLAIMS']) {
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
    ...webauthnEnv(env, external, site, redirects),
    ...passthrough,
    GOTRUE_API_HOST: gotrue.host,
    PORT: String(gotrue.port),
    API_EXTERNAL_URL: api,
    GOTRUE_SITE_URL: site,
    // The hosted pages are always a valid redirect target (password reset).
    // GoTrue checks the consent page's requests against this list by origin.
    GOTRUE_URI_ALLOW_LIST: [...redirects, `${external}/ui/**`, ...(env.AUTH_OAUTH_SERVER === '1' ? [external] : [])].join(','),
    GOTRUE_DB_DRIVER: 'postgres',
    DATABASE_URL: databaseURL(env.DATABASE_URL),
    DB_NAMESPACE: 'auth',
    GOTRUE_DB_MAX_POOL_SIZE: String(limits.pool),
    GOTRUE_JWT_SECRET: env.AUTH_JWT_SECRET,
    GOTRUE_JWT_KEYS: keys.gotrue,
    GOTRUE_JWT_EXP: String(limits.accessTokenSeconds),
    GOTRUE_JWT_AUD: 'authenticated',
    // GoTrue stores this as each new user's role, and a token's role claim
    // is the user's role; unset, every token says role "". v2.197.0 calls it
    // deprecated at start (logs.mjs drops that line) but still applies it;
    // check it on any GoTrue upgrade. sql/0012 fixes users made before.
    GOTRUE_JWT_DEFAULT_GROUP_NAME: 'authenticated',
    GOTRUE_JWT_ISSUER: api,
    GOTRUE_JWT_ADMIN_ROLES: 'service_role',
    GOTRUE_EXTERNAL_EMAIL_ENABLED: passthrough.GOTRUE_EXTERNAL_EMAIL_ENABLED ?? 'true',
    GOTRUE_MAILER_AUTOCONFIRM: env.AUTH_AUTOCONFIRM === '1' ? 'true' : 'false',
    GOTRUE_DISABLE_SIGNUP: env.AUTH_DISABLE_SIGNUP === '1' ? 'true' : 'false',
    GOTRUE_SECURITY_REFRESH_TOKEN_ROTATION_ENABLED: 'true',
    // Signed-in users may link and unlink sign-in methods (and anonymous
    // users keep their data by linking one).
    GOTRUE_SECURITY_MANUAL_LINKING_ENABLED: env.AUTH_IDENTITY_LINKING === '1' ? 'true' : 'false',
    // The public gateway replaces X-Real-IP with the verified client address.
    GOTRUE_RATE_LIMIT_HEADER: 'X-Real-IP',
    // GoTrue v2.197.0 does not apply this to its request and audit logs,
    // which carry user email addresses; logs.mjs filters them as well.
    GOTRUE_LOG_LEVEL: 'warn',
  }
  const email = emailConfig(env)
  const branding = brandingConfig(env, site)
  const sessions = sessionPolicy(env)
  Object.assign(gotrueEnv, smsEnv(passthrough, branding.name), samlEnv(env), phoneMFAEnv(passthrough, branding.name), oauthEnv(env), sessions.gotrueEnv)
  const captcha = captchaConfig(env)
  const retention = env.AUTH_AUDIT_RETENTION_DAYS ?? ''
  if (retention !== '' && (!/^[1-9][0-9]{0,3}$/.test(retention) || Number(retention) > limits.auditRetentionMaxDays)) throw new Error(`AUTH_AUDIT_RETENTION_DAYS must be 1 to ${limits.auditRetentionMaxDays}`)
  const anonymousRetention = env.AUTH_ANONYMOUS_RETENTION_DAYS ?? ''
  if (anonymousRetention !== '' && (!/^[1-9][0-9]{0,3}$/.test(anonymousRetention) || Number(anonymousRetention) > limits.anonymousRetentionMaxDays)) throw new Error(`AUTH_ANONYMOUS_RETENTION_DAYS must be 1 to ${limits.anonymousRetentionMaxDays}`)
  const config = { external, api, gotrueEnv, redirects, keys: { signing: keys.signing, verifying: keys.verifying }, authz: authzConfig(env.AUTH_AUTHZ), origins: browserOrigins(site, redirects), email, app: branding.name, branding, captcha, lifecycleEvents: env.AUTH_LIFECYCLE_EVENTS === '1', auditRetentionDays: retention === '' ? null : Number(retention), smsCountries: smsCountries(env),
    // Self-service organization SSO needs SSO itself.
    orgSSO: env.AUTH_ORG_SSO === '1' && gotrueEnv.GOTRUE_SAML_ENABLED === 'true',
    oauth: env.AUTH_OAUTH_SERVER === '1', selfDeletion: env.AUTH_SELF_DELETION === '1', orgClaims: env.AUTH_ORG_CLAIMS === '1', sessions: sessions.policy, anonymousRetentionDays: anonymousRetention === '' ? null : Number(anonymousRetention), productControlTeamEnabled: env.AUTH_PRODUCT_CONTROL_TEAM_ENABLED === '1', productControlEmailEnabled: env.AUTH_PRODUCT_CONTROL_EMAIL_ENABLED === '1' }
  return { ...config, settings: publicSettings(config) }
}

// publicSettings is what /settings/v1 reports: only values that are public
// anyway (published key IDs, enabled providers, rules a sign-up form shows)
// or harmless. The CLI and dashboard read settings here because app secrets
// are write-only.
export function publicSettings({ gotrueEnv: g, redirects, keys, email, app, branding, captcha, lifecycleEvents, auditRetentionDays, smsCountries: countries, orgSSO, anonymousRetentionDays, selfDeletion, orgClaims, sessions }) {
  const passkeys = g.GOTRUE_PASSKEY_ENABLED === 'true'
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
    // GoTrue's defaults: TOTP on, SMS codes and recovery codes off.
    mfa: { totp: flag(g.GOTRUE_MFA_TOTP_ENROLL_ENABLED, true), phone: flag(g.GOTRUE_MFA_PHONE_ENROLL_ENABLED, false), recovery_codes: flag(g.GOTRUE_MFA_RECOVERY_CODES_ENROLL_ENABLED, false) },
    email: { configured: email !== null, provider: email?.provider ?? null, from: email?.from ?? null },
    events: { lifecycle: lifecycleEvents },
    activity: { retention_days: auditRetentionDays },
    // Anonymous users idle longer than retention_days are deleted.
    anonymous: { enabled: g.GOTRUE_EXTERNAL_ANONYMOUS_USERS_ENABLED === 'true', retention_days: anonymousRetentionDays },
    identity_linking: g.GOTRUE_SECURITY_MANUAL_LINKING_ENABLED === 'true',
    // Session limits GoTrue enforces at refresh; null means none.
    sessions: { max_age_minutes: sessions?.maxAgeMinutes ?? null, idle_minutes: sessions?.idleMinutes ?? null, single_per_user: sessions?.singlePerUser === true },
    // Signed-in users may delete their own accounts (/authz/v1/account).
    self_deletion: selfDeletion === true,
    // Access tokens carry the session's active organization (org_* claims).
    organization_claims: orgClaims === true,
    passkeys: { enabled: passkeys, rp_id: g.GOTRUE_WEBAUTHN_RP_ID, origins: g.GOTRUE_WEBAUTHN_RP_ORIGINS.split(',').filter(Boolean) },
    branding: { name: branding.name, color: branding.color, logo_url: branding.logoURL },
    captcha: captcha ? { provider: captcha.provider, site_key: captcha.siteKey } : null,
    phone: { enabled: g.GOTRUE_EXTERNAL_PHONE_ENABLED === 'true', provider: g.GOTRUE_EXTERNAL_PHONE_ENABLED === 'true' ? g.GOTRUE_SMS_PROVIDER : null, countries },
    // What an identity provider needs to trust this app: GoTrue uses its
    // metadata URL as the service provider's entity ID.
    sso: g.GOTRUE_SAML_ENABLED === 'true'
      ? { enabled: true, entity_id: `${g.API_EXTERNAL_URL}/sso/saml/metadata`, acs_url: `${g.API_EXTERNAL_URL}/sso/saml/acs`, self_service: Boolean(orgSSO) }
      : { enabled: false, entity_id: null, acs_url: null, self_service: false },
    // What a client or an MCP gateway needs to use this app as its
    // authorization server.
    oauth: g.GOTRUE_OAUTH_SERVER_ENABLED === 'true'
      ? { enabled: true, dynamic_registration: g.GOTRUE_OAUTH_SERVER_ALLOW_DYNAMIC_REGISTRATION === 'true', issuer: g.GOTRUE_JWT_ISSUER,
        metadata_url: `${new URL(g.GOTRUE_JWT_ISSUER).origin}/.well-known/oauth-authorization-server/auth/v1`, default_audience: oauthAudience }
      : { enabled: false, dynamic_registration: false, issuer: null, metadata_url: null, default_audience: null },
    keys,
  }
}

// phoneMFAEnv: SMS codes as a second factor are sent through the SMS
// provider. Without one, enrolment stays off rather than creating factors no
// code can ever reach; verification is left as configured, so enrolled users
// recover once a provider is set again. The message names the app.
export function phoneMFAEnv(passthrough, appName) {
  if (passthrough.GOTRUE_MFA_PHONE_ENROLL_ENABLED !== 'true') return {}
  if (!smsProviders.includes(passthrough.GOTRUE_SMS_PROVIDER)) {
    console.error(JSON.stringify({ level: 'warning', event: 'phone_mfa_without_sms_provider' }))
    return { GOTRUE_MFA_PHONE_ENROLL_ENABLED: 'false' }
  }
  return passthrough.GOTRUE_MFA_PHONE_TEMPLATE ? {} : { GOTRUE_MFA_PHONE_TEMPLATE: `Your ${appName.replace(/[{}]/g, '')} verification code is {{ .Code }}` }
}

// smsEnv checks phone sign-in's provider. Unless the owner set them, codes
// last five minutes instead of GoTrue's 60 seconds, which a delayed text
// can outlast, and the message names the app. GoTrue parses the message as a
// Go template, so braces are dropped from the name.
export function smsEnv(passthrough, appName) {
  if (passthrough.GOTRUE_EXTERNAL_PHONE_ENABLED !== 'true') return {}
  if (!smsProviders.includes(passthrough.GOTRUE_SMS_PROVIDER)) throw new Error(`GOTRUE_SMS_PROVIDER must be one of ${smsProviders.join(', ')} when phone sign-in is on`)
  return {
    ...(passthrough.GOTRUE_SMS_OTP_EXP ? {} : { GOTRUE_SMS_OTP_EXP: String(limits.smsCodeSeconds) }),
    ...(passthrough.GOTRUE_SMS_TEMPLATE ? {} : { GOTRUE_SMS_TEMPLATE: `Your ${appName.replace(/[{}]/g, '')} code is {{ .Code }}` }),
  }
}

// samlEnv turns on GoTrue's SAML service provider for organization single
// sign-on. AUTH_SAML_PRIVATE_KEY is the RSA key GoTrue signs requests with
// and publishes in its metadata, as GoTrue requires it: base64 PKCS#1 DER,
// at least 2048 bits, exponent 65537. It stays sealed when SSO is turned off,
// so turning it back on keeps every identity provider's trust.
export function samlEnv(env) {
  if (env.AUTH_SSO !== '1') return { GOTRUE_SAML_ENABLED: 'false' }
  const value = env.AUTH_SAML_PRIVATE_KEY ?? ''
  let key
  try {
    if (!/^[A-Za-z0-9+/]+={0,2}$/.test(value)) throw new Error('not base64')
    key = createPrivateKey({ key: Buffer.from(value, 'base64'), format: 'der', type: 'pkcs1' })
  } catch { throw new Error('AUTH_SSO requires AUTH_SAML_PRIVATE_KEY, a base64 PKCS#1 RSA key') }
  const details = key.asymmetricKeyDetails
  if (key.asymmetricKeyType !== 'rsa' || details.modulusLength < 2048 || details.publicExponent !== 65537n) throw new Error('AUTH_SAML_PRIVATE_KEY must be an RSA key of at least 2048 bits with exponent 65537')
  return { GOTRUE_SAML_ENABLED: 'true', GOTRUE_SAML_PRIVATE_KEY: value }
}

// oauthEnv turns on GoTrue's OAuth 2.1 authorization server. Open (dynamic)
// client registration is a second, separate choice; it is ignored while the
// server is off so turning the server off never fails startup.
export function oauthEnv(env) {
  if (env.AUTH_OAUTH_SERVER !== '1') return { GOTRUE_OAUTH_SERVER_ENABLED: 'false' }
  return {
    GOTRUE_OAUTH_SERVER_ENABLED: 'true',
    GOTRUE_OAUTH_SERVER_ALLOW_DYNAMIC_REGISTRATION: env.AUTH_OAUTH_REGISTRATION === '1' ? 'true' : 'false',
    GOTRUE_OAUTH_SERVER_AUTHORIZATION_PATH: consentPath,
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

// sessionPolicy reads the owner's session limits: a maximum age from sign-in
// and an inactivity timeout, in minutes, and whether a user keeps only their
// most recently refreshed session. GoTrue enforces them when a session
// refreshes, so the bounds start at 15 minutes, above an access token's
// ten-minute lifetime.
export function sessionPolicy(env) {
  const minutes = name => {
    const value = env[name] ?? ''
    if (value === '') return null
    if (!/^[1-9][0-9]{1,5}$/.test(value) || Number(value) < limits.sessionMinMinutes || Number(value) > limits.sessionMaxMinutes) {
      throw new Error(`${name} must be ${limits.sessionMinMinutes} to ${limits.sessionMaxMinutes} minutes`)
    }
    return Number(value)
  }
  // An idle limit longer than the maximum age has no effect; it is not an
  // error, because the two are set separately and a refusal here would stop
  // the app from starting.
  const maxAge = minutes('AUTH_SESSION_MAX_AGE_MINUTES'), idle = minutes('AUTH_SESSION_IDLE_MINUTES')
  if (env.AUTH_SINGLE_SESSION !== undefined && env.AUTH_SINGLE_SESSION !== '1' && env.AUTH_SINGLE_SESSION !== '0') throw new Error('Invalid AUTH_SINGLE_SESSION')
  const single = env.AUTH_SINGLE_SESSION === '1'
  return {
    policy: { maxAgeMinutes: maxAge, idleMinutes: idle, singlePerUser: single },
    gotrueEnv: {
      ...(maxAge !== null ? { GOTRUE_SESSIONS_TIMEBOX: `${maxAge}m` } : {}),
      ...(idle !== null ? { GOTRUE_SESSIONS_INACTIVITY_TIMEOUT: `${idle}m` } : {}),
      ...(single ? { GOTRUE_SESSIONS_SINGLE_PER_USER: 'true' } : {}),
    },
  }
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
