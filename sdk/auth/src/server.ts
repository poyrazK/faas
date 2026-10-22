// @gregale/auth/server: verify application Auth access tokens in a Node,
// Deno, Bun or edge backend and check permissions (ADR-1190). It follows the
// same rules as the Go (sdk/go/auth) and Python (faas_sdk.auth) SDKs and
// uses only WebCrypto and fetch.
//
//   const auth = createVerifier({ url: 'https://shop-auth.gregale.app' })
//   const claims = await auth.verify(bearerToken(request.headers.get('authorization')))
//   const allowed = await auth.authorize(token, 'read', resource(org, 'doc', id))
//
// verify checks a token's ES256 signature against the Auth app's published
// keys, its issuer, its audience and its expiry, without a network call once
// the keys are cached. A token stays valid until it expires, at most 600
// seconds after a sign-out or ban; authorize asks the Auth app, which also
// checks that the session is still active.
import { AuthError, resource } from './index.js'
import { baseURL } from './url.js'

export { AuthError, resource }

export const AUDIENCE = 'authenticated'
/** Clock-skew tolerance for exp and nbf, as Gregale's edge JWT rules use. */
export const DEFAULT_LEEWAY_SECONDS = 60

// Gregale's verifiers cache the key set for five minutes and refetch it for
// an unknown key ID at most once every 30 seconds.
const jwksTTLMs = 300_000
const jwksMinRefetchMs = 30_000
const maxTokenLength = 16 << 10
const maxBodyBytes = 64 << 10

export type VerifiedClaims = Readonly<{
  sub: string
  sessionId?: string
  role?: string
  email?: string
  phone?: string
  aal?: string
  isAnonymous: boolean
  /** Unix seconds. */
  expiresAt: number
  issuedAt?: number
  appMetadata: Readonly<Record<string, unknown>>
  userMetadata: Readonly<Record<string, unknown>>
  /**
   * The session's active organization, the user's role in it and that
   * role's permissions, when the Auth app has organization claims on. A
   * changed membership shows at the next token, within its lifetime.
   */
  organization?: Readonly<{ id: string; slug: string; role: string; permissions: readonly string[] }>
  /**
   * Set while the app's support staff use this session as the user
   * (impersonation): who opened it, such as "admin:0A1B2C3D". Refuse
   * actions the person must take themselves, such as payments.
   */
  actor?: string
  /**
   * Set when the token belongs to an organization's service account (the
   * client-credentials grant), not a person: id is the account's ID (also
   * sub) and organization is the account's. scope lists the permissions the
   * token was narrowed to, when it was. authorize() applies the account's
   * role and stops working at once when the account or its secret is
   * revoked; verify() alone accepts the token until it expires.
   */
  serviceAccount?: Readonly<{ id: string; secretId: string; scope?: readonly string[] }>
  /** Every claim, including ones this type does not name. */
  raw: Readonly<Record<string, unknown>>
}>

// actorOf reads RFC 8693's act claim, which the Auth app sets on sessions
// its operator opened as a user.
function actorOf(act: unknown): { actor?: string } {
  const sub = act !== null && typeof act === 'object' ? (act as { sub?: unknown }).sub : undefined
  return typeof sub === 'string' && sub !== '' ? { actor: sub } : {}
}

function serviceAccountOf(payload: Record<string, unknown>): { serviceAccount?: VerifiedClaims['serviceAccount'] } {
  if (payload.service_account !== true || typeof payload.sub !== 'string' || typeof payload.secret_id !== 'string') return {}
  const scope = typeof payload.scope === 'string' ? { scope: payload.scope.split(' ').filter(Boolean) } : {}
  return { serviceAccount: { id: payload.sub, secretId: payload.secret_id, ...scope } }
}

export type VerifierOptions = {
  /** The Auth app's origin, such as https://shop-auth.gregale.app. */
  url: string
  fetch?: typeof fetch
  leewaySeconds?: number
  /** Milliseconds since the epoch; tests replace it. */
  now?: () => number
}

const invalid = (detail: string) => Object.assign(new AuthError(401, 'invalid_token'), { message: `invalid_token: ${detail}` })

function decode(segment: string): Uint8Array<ArrayBuffer> {
  if (!/^[A-Za-z0-9_-]*$/.test(segment)) throw invalid('malformed token')
  const base64 = segment.replace(/-/g, '+').replace(/_/g, '/') + '='.repeat((4 - segment.length % 4) % 4)
  return Uint8Array.from(atob(base64), c => c.charCodeAt(0))
}

function decodeJSON(segment: string): Record<string, unknown> {
  let value: unknown
  try { value = JSON.parse(new TextDecoder().decode(decode(segment))) } catch { throw invalid('malformed token') }
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw invalid('malformed token')
  return value as Record<string, unknown>
}

const num = (value: unknown) => typeof value === 'number' && Number.isFinite(value) ? value : undefined
const str = (value: unknown) => typeof value === 'string' ? value : undefined
const obj = (value: unknown) => value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {}

/** Returns the token from an `Authorization: Bearer` header value. */
export function bearerToken(header: string | null | undefined): string {
  const match = /^Bearer\s+(\S+)$/i.exec(header ?? '')
  if (!match) throw new AuthError(401, 'no_token')
  return match[1]
}

type KeySet = { keys: Map<string, CryptoKey>; fetchedAt: number }

export function createVerifier(options: VerifierOptions) {
  const origin = baseURL(options.url)
  const issuer = `${origin}/auth/v1`
  const fetchImpl = options.fetch ?? globalThis.fetch.bind(globalThis)
  const leeway = options.leewaySeconds ?? DEFAULT_LEEWAY_SECONDS
  const now = options.now ?? Date.now
  if (leeway < 0) throw new Error('leewaySeconds must not be negative')
  let cached: KeySet | null = null
  let attemptAt = -Infinity
  let lastError: AuthError | null = null
  let fetching: Promise<void> | null = null

  async function fetchKeys(): Promise<Map<string, CryptoKey>> {
    let response: Response
    try {
      response = await fetchImpl(`${issuer}/.well-known/jwks.json`, { headers: { Accept: 'application/json' }, redirect: 'error', signal: AbortSignal.timeout(10_000) })
    } catch {
      throw new AuthError(503, 'auth_unavailable')
    }
    const text = await response.text().catch(() => '')
    if (!response.ok || text.length > maxBodyBytes) throw new AuthError(503, 'auth_unavailable')
    let set: { keys?: unknown }
    try { set = JSON.parse(text) } catch { throw new AuthError(503, 'auth_unavailable') }
    const keys = new Map<string, CryptoKey>()
    for (const jwk of Array.isArray(set.keys) ? set.keys : []) {
      if (!jwk || jwk.kty !== 'EC' || jwk.crv !== 'P-256' || typeof jwk.kid !== 'string' || !jwk.kid) continue
      if ((jwk.alg !== undefined && jwk.alg !== 'ES256') || (jwk.use !== undefined && jwk.use !== 'sig')) continue
      if (jwk.key_ops !== undefined && !(Array.isArray(jwk.key_ops) && jwk.key_ops.includes('verify'))) continue
      try {
        keys.set(jwk.kid, await crypto.subtle.importKey('jwk', { kty: 'EC', crv: 'P-256', x: jwk.x, y: jwk.y }, { name: 'ECDSA', namedCurve: 'P-256' }, false, ['verify']))
      } catch { /* not a point on the curve */ }
    }
    if (keys.size === 0) throw new AuthError(503, 'auth_unavailable')
    return keys
  }

  // refresh fetches the key set at most once every 30 seconds; concurrent
  // callers share one fetch. A failed refresh keeps cached keys working.
  function refresh(): Promise<void> {
    if (now() - attemptAt < jwksMinRefetchMs) return fetching ?? Promise.resolve()
    attemptAt = now()
    fetching = fetchKeys().then(keys => { cached = { keys, fetchedAt: now() }; lastError = null }, err => { lastError = err })
      .finally(() => { fetching = null })
    return fetching
  }

  async function key(kid: string): Promise<CryptoKey> {
    const fresh = cached && now() - cached.fetchedAt < jwksTTLMs
    const known = cached?.keys.get(kid)
    if (known && fresh) return known
    await refresh()
    const found = cached?.keys.get(kid)
    if (found) return found
    if (!cached && lastError) throw new AuthError(lastError.status, lastError.code)
    throw invalid(`unknown signing key ${kid}`)
  }

  /** Verifies token and returns its claims; throws AuthError 401 or 503. */
  async function verify(token: string): Promise<VerifiedClaims> {
    if (typeof token !== 'string' || token.length > maxTokenLength) throw invalid('not a JWT')
    const parts = token.split('.')
    if (parts.length !== 3) throw invalid('not a JWT')
    const header = decodeJSON(parts[0])
    if (header.alg !== 'ES256' || typeof header.kid !== 'string' || !header.kid || 'crit' in header) throw invalid('header must name an ES256 key')
    const signature = decode(parts[2])
    if (signature.length !== 64) throw invalid('malformed signature')
    const verifier = await key(header.kid)
    const ok = await crypto.subtle.verify({ name: 'ECDSA', hash: 'SHA-256' }, verifier, signature, new TextEncoder().encode(`${parts[0]}.${parts[1]}`))
    if (!ok) throw invalid('bad signature')
    return claims(decodeJSON(parts[1]))
  }

  function claims(payload: Record<string, unknown>): VerifiedClaims {
    const seconds = now() / 1000
    const aud = payload.aud
    const exp = num(payload.exp), nbf = num(payload.nbf)
    if (payload.iss !== issuer) throw invalid(`issuer is not ${issuer}`)
    if (!(aud === AUDIENCE || (Array.isArray(aud) && aud.includes(AUDIENCE)))) throw invalid(`audience is not ${AUDIENCE}`)
    if (typeof payload.sub !== 'string' || !payload.sub) throw invalid('no subject')
    if (exp === undefined) throw invalid('no expiry')
    if (seconds > exp + leeway) throw new AuthError(401, 'token_expired')
    if ('nbf' in payload && (nbf === undefined || seconds + leeway < nbf)) throw invalid('not valid yet')
    return {
      sub: payload.sub, sessionId: str(payload.session_id), role: str(payload.role), email: str(payload.email), phone: str(payload.phone),
      aal: str(payload.aal), isAnonymous: payload.is_anonymous === true, expiresAt: exp, issuedAt: num(payload.iat),
      appMetadata: obj(payload.app_metadata), userMetadata: obj(payload.user_metadata), raw: payload,
      ...actorOf(payload.act),
      ...serviceAccountOf(payload),
      ...(typeof payload.org_id === 'string' && typeof payload.org_slug === 'string' && typeof payload.org_role === 'string'
        ? { organization: { id: payload.org_id, slug: payload.org_slug, role: payload.org_role, permissions: Array.isArray(payload.org_permissions) ? payload.org_permissions.filter((p): p is string => typeof p === 'string') : [] } }
        : {}),
    }
  }

  /**
   * Asks the Auth app whether token's user may take action on resource. The
   * Auth app checks that the session is still active, so a signed-out or
   * banned user is refused at once.
   */
  async function authorize(token: string, action: string, target: string): Promise<boolean> {
    let response: Response
    try {
      response = await fetchImpl(`${origin}/authz/v1/authorize`, {
        method: 'POST', redirect: 'error', signal: AbortSignal.timeout(10_000),
        headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' }, body: JSON.stringify({ action, resource: target }),
      })
    } catch {
      throw new AuthError(503, 'auth_unavailable')
    }
    let body: { allowed?: unknown; code?: unknown } | null = null
    try { body = await response.json() } catch { body = null }
    if (!response.ok) {
      const code = typeof body?.code === 'string' && /^[a-z0-9_]{1,64}$/.test(body.code) ? body.code : `http_${response.status}`
      throw new AuthError(response.status, code)
    }
    return body?.allowed === true
  }

  return { issuer, verify, authorize }
}

export type Verifier = ReturnType<typeof createVerifier>

export type ClientCredentialsOptions = {
  /** The Auth app's origin, such as https://shop-auth.gregale.app. */
  url: string
  /** The service account's ID. */
  clientId: string
  /** One of its secrets (gasa_...). Keep it out of browsers. */
  clientSecret: string
  /** Permissions to narrow the token to; each must be one its role holds. */
  scope?: readonly string[]
  fetch?: typeof fetch
  /** Milliseconds since the epoch; tests replace it. */
  now?: () => number
}

// A token is fetched again this long before it expires, so a request that
// carries it still finds it valid.
const refreshMarginMs = 60_000

/**
 * Gets access tokens for an organization's service account with the
 * client-credentials grant, for a backend calling an API or the Data API
 * as that organization. token() caches one until a minute before it
 * expires; concurrent callers share one request.
 *
 *   const machine = createClientCredentials({ url, clientId, clientSecret })
 *   await fetch(api, { headers: { Authorization: `Bearer ${await machine.token()}` } })
 */
export function createClientCredentials(options: ClientCredentialsOptions) {
  const origin = baseURL(options.url)
  const fetchImpl = options.fetch ?? globalThis.fetch.bind(globalThis)
  const now = options.now ?? Date.now
  if (!/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(options.clientId)) throw new Error('clientId must be a service account ID')
  if (!/^gasa_[A-Za-z0-9_-]{43}$/.test(options.clientSecret)) throw new Error('clientSecret must be a service account secret')
  const form = new URLSearchParams({ grant_type: 'client_credentials', ...(options.scope?.length ? { scope: options.scope.join(' ') } : {}) })
  const basic = `Basic ${btoa(`${encodeURIComponent(options.clientId)}:${encodeURIComponent(options.clientSecret)}`)}`
  let cached: { token: string; refreshAt: number } | null = null
  let pending: Promise<string> | null = null

  async function request(): Promise<string> {
    let response: Response
    try {
      response = await fetchImpl(`${origin}/auth/v1/oauth/token`, {
        method: 'POST', redirect: 'error', signal: AbortSignal.timeout(10_000),
        headers: { Authorization: basic, 'Content-Type': 'application/x-www-form-urlencoded', Accept: 'application/json' }, body: form.toString(),
      })
    } catch {
      throw new AuthError(503, 'auth_unavailable')
    }
    let body: { access_token?: unknown; expires_in?: unknown; error?: unknown } | null = null
    try { body = await response.json() } catch { body = null }
    if (!response.ok || typeof body?.access_token !== 'string' || typeof body.expires_in !== 'number') {
      const code = typeof body?.error === 'string' && /^[a-z0-9_]{1,64}$/.test(body.error) ? body.error : `http_${response.status}`
      throw new AuthError(response.ok ? 502 : response.status, code)
    }
    cached = { token: body.access_token, refreshAt: now() + body.expires_in * 1000 - refreshMarginMs }
    return body.access_token
  }

  /** Returns a valid access token, fetching a new one when needed. */
  function token(): Promise<string> {
    if (cached && now() < cached.refreshAt) return Promise.resolve(cached.token)
    pending ??= request().finally(() => { pending = null })
    return pending
  }

  /** Drops the cached token, for example after an API answered 401. */
  function clear() { cached = null }

  return { token, clear }
}

export type ClientCredentials = ReturnType<typeof createClientCredentials>
