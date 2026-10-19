// @gregale/auth: client for a Gregale application Auth app (ADR-1190).
// Sign-up, sign-in, passkeys, sessions with single-flight refresh, the hosted
// pages, and the /authz/v1 organization and authorization API. No
// dependencies.
import { aal, activeOrganization, claims, type ActiveOrganization } from './claims.js'
import { baseURL } from './url.js'

export type Session = Readonly<{
  accessToken: string
  refreshToken: string
  /** Unix seconds when the access token expires. */
  expiresAt: number
  user?: User
}>

export type User = Readonly<{ id: string; email?: string; [key: string]: unknown }>

/** Where the session is kept between page loads. `null` keeps it in memory only. */
export type SessionStore = {
  get(): string | null | Promise<string | null>
  set(value: string): void | Promise<void>
  remove(): void | Promise<void>
}

export type AuthClientOptions = {
  /** The Auth app's origin, for example https://shop-auth.gregale.app. */
  url: string
  fetch?: typeof globalThis.fetch
  /** Defaults to localStorage in browsers and memory elsewhere. */
  store?: SessionStore | null
  /** Refresh this many seconds before the access token expires (default 60). */
  refreshMarginSeconds?: number
  /** WebAuthn for passkeys; defaults to navigator.credentials in browsers. */
  credentials?: Pick<CredentialsContainer, 'get' | 'create'>
}

/** A passkey registered to the signed-in user. */
export type Passkey = Readonly<{ id: string; friendly_name?: string | null; created_at?: string; last_used_at?: string | null; [key: string]: unknown }>

/** Sign-up and sign-in calls take the CAPTCHA token when the Auth app requires one. */
export type Captcha = { captchaToken?: string }

export type AuthEvent = 'SIGNED_IN' | 'TOKEN_REFRESHED' | 'SIGNED_OUT' | 'MFA_VERIFIED'

/** A second factor: an authenticator app, a phone, a passkey or recovery codes. */
export type Factor = Readonly<{ id: string; factor_type: string; status: string; friendly_name?: string; phone?: string; created_at?: string; [key: string]: unknown }>
export type TOTPEnrollment = Readonly<{ id: string; type: 'totp'; friendly_name?: string; totp: Readonly<{ qr_code: string; secret: string; uri: string }> }>
export type FactorChallenge = Readonly<{ id: string; type: string; expires_at?: number }>
export type RecoveryCodes = Readonly<{ id: string; type: string; total: number; remaining?: number; codes?: readonly string[] }>
export type AssuranceLevel = Readonly<{ current: 'aal1' | 'aal2' | null; next: 'aal1' | 'aal2' | null }>

export type { ActiveOrganization } from './claims.js'

/** A third-party app (an OAuth client) the user allowed to use their account. */
export type ConnectedApp = Readonly<{ client: { id: string; name: string; uri?: string; logo_uri?: string }; scopes: string[]; granted_at: string }>
/** One of the signed-in user's own sessions; current is the one making the call. */
export type UserSession = Readonly<{ id: string; created_at: string; last_active_at: string; user_agent: string | null; ip: string | null; aal: string | null; current: boolean }>

/**
 * mfa_required: the organization's permissions hold only in sessions
 * verified with a second factor (aal2); requests from others fail with
 * mfa_required.
 */
export type Organization = Readonly<{ id: string; slug: string; name: string; role?: string; created_at: string; mfa_required?: boolean }>
/**
 * One entry of an organization's audit log. actor_id is null for changes the
 * app's operator, an identity provider or SCIM made.
 */
export type AuditEntry = Readonly<{
  id: string; occurred_at: string
  action: 'organization.created' | 'member.added' | 'member.role_changed' | 'member.removed' | 'member.signed_in' | 'invitation.created' | 'invitation.revoked'
    | 'invitation.accepted' | 'domain.added' | 'domain.verified' | 'domain.removed' | 'sso.connected' | 'sso.updated' | 'sso.disconnected'
  actor_id: string | null; actor_email: string | null; target_user_id: string | null; target_email: string | null; data: Readonly<Record<string, unknown>>
}>
/** mfa_enabled (a verified second factor) is shown only to those who manage members, null to others. */
export type Member = Readonly<{ user_id: string; email: string; role: string; created_at: string; mfa_enabled?: boolean | null }>
export type Invitation = Readonly<{ id: string; email: string; role: string; expires_at: string; created_at?: string }>
/** An email domain an organization proves it controls with a DNS TXT record. */
/** How people at a verified domain join its organization: not at all, offered to them, or at once. */
export type DomainJoinMode = 'off' | 'suggest' | 'automatic'
export type OrgDomain = Readonly<{ domain: string; verified: boolean; verified_at: string | null; record: Readonly<{ type: 'TXT'; name: string; value: string }>; join: Readonly<{ mode: DomainJoinMode; role: string }> }>
/** An organization the user may join because it verified the user's email domain. */
export type SuggestedOrganization = Readonly<{ id: string; slug: string; name: string; domain: string; role: string }>
export type OrgSSOProvider = Readonly<{ id: string; entity_id: string | null; metadata_url: string | null; domains: readonly string[]; role: string }>
export type CreatedInvitation = Invitation & Readonly<{ delivered: boolean; token?: string }>

/** The wallets GoTrue verifies sign-ins from (EIP-4361 and Sign-In With Solana). */
export type Web3Chain = 'ethereum' | 'solana'
/** An injected Ethereum wallet (EIP-1193), such as window.ethereum. */
export type EthereumWallet = { request(args: { method: string; params?: unknown[] }): Promise<unknown> }
/** An injected Solana wallet, such as window.solana or window.phantom.solana. */
export type SolanaWallet = {
  publicKey?: { toBase58(): string } | null
  connect(): Promise<unknown>
  signMessage(message: Uint8Array, encoding?: string): Promise<Uint8Array | { signature: Uint8Array }>
}

/**
 * web3Message writes the message a wallet signs, in the grammar GoTrue's
 * parsers accept (EIP-4361 for Ethereum; its Solana equivalent). GoTrue
 * checks that uri is the app's site URL or an allowed redirect URL, that
 * domain is uri's host, and that the message is at most ten minutes old.
 */
export function web3Message(chain: Web3Chain, input: { domain: string; address: string; uri: string; statement?: string; chainId?: number; nonce: string; issuedAt: string }): string {
  const name = chain === 'ethereum' ? 'Ethereum' : 'Solana'
  return [
    `${input.domain} wants you to sign in with your ${name} account:`, input.address, '',
    ...(input.statement ? [input.statement.replace(/[\r\n]+/g, ' '), ''] : []),
    `URI: ${input.uri}`, 'Version: 1', ...(input.chainId !== undefined ? [`Chain ID: ${input.chainId}`] : []), `Nonce: ${input.nonce}`, `Issued At: ${input.issuedAt}`,
  ].join('\n')
}

const hexOf = (bytes: Uint8Array) => Array.from(bytes, b => b.toString(16).padStart(2, '0')).join('')
const base64Of = (bytes: Uint8Array) => btoa(String.fromCharCode(...bytes))

/** A failed request. `code` is the stable server code, or `http_<status>`. */
export class AuthError extends Error {
  readonly status: number
  readonly code: string
  constructor(status: number, code: string) {
    super(code)
    this.name = 'AuthError'
    this.status = status
    this.code = code
  }
}

const storageKey = 'gregale.auth.session'

function memoryStore(): SessionStore {
  let value: string | null = null
  return { get: () => value, set: v => { value = v }, remove: () => { value = null } }
}

function defaultStore(): SessionStore {
  const storage = (globalThis as { localStorage?: Storage }).localStorage
  if (!storage) return memoryStore()
  return { get: () => storage.getItem(storageKey), set: v => storage.setItem(storageKey, v), remove: () => storage.removeItem(storageKey) }
}


type TokenResponse = { access_token: string; refresh_token: string; expires_in: number; expires_at?: number; user?: User }

function toSession(body: TokenResponse): Session {
  if (typeof body?.access_token !== 'string' || typeof body.refresh_token !== 'string') throw new AuthError(502, 'invalid_session')
  const expiresAt = Number(body.expires_at) || Math.floor(Date.now() / 1000) + Number(body.expires_in)
  if (!Number.isFinite(expiresAt)) throw new AuthError(502, 'invalid_session')
  return Object.freeze({ accessToken: body.access_token, refreshToken: body.refresh_token, expiresAt, ...(body.user ? { user: body.user } : {}) })
}

// WebAuthn options arrive as JSON with base64url buffers; browsers want
// ArrayBuffers. Mirrors the hosted pages (templates/auth/ui/app.js).
function fromBase64url(value: string): ArrayBuffer {
  const base64 = value.replace(/-/g, '+').replace(/_/g, '/') + '='.repeat((4 - value.length % 4) % 4)
  return Uint8Array.from(atob(base64), c => c.charCodeAt(0)).buffer
}

function toBase64url(buffer: ArrayBuffer): string {
  return btoa(String.fromCharCode(...new Uint8Array(buffer))).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

type CredentialOptionsJSON = { challenge: string; user?: { id: string; [key: string]: unknown }; allowCredentials?: { id: string }[]; excludeCredentials?: { id: string }[]; [key: string]: unknown }

function decodeOptions(options: CredentialOptionsJSON): any {
  const ids = (list?: { id: string }[]) => list?.map(c => ({ ...c, id: fromBase64url(c.id) }))
  return {
    ...options, challenge: fromBase64url(options.challenge),
    ...(options.user ? { user: { ...options.user, id: fromBase64url(options.user.id) } } : {}),
    ...(options.allowCredentials ? { allowCredentials: ids(options.allowCredentials) } : {}),
    ...(options.excludeCredentials ? { excludeCredentials: ids(options.excludeCredentials) } : {}),
  }
}

function credentialJSON(credential: any): unknown {
  if (typeof credential.toJSON === 'function') return credential.toJSON()
  const r = credential.response
  const response: Record<string, unknown> = { clientDataJSON: toBase64url(r.clientDataJSON) }
  if (r.attestationObject) Object.assign(response, { attestationObject: toBase64url(r.attestationObject), transports: r.getTransports?.() ?? [] })
  if (r.authenticatorData) Object.assign(response, { authenticatorData: toBase64url(r.authenticatorData), signature: toBase64url(r.signature), ...(r.userHandle ? { userHandle: toBase64url(r.userHandle) } : {}) })
  return { id: credential.id, rawId: toBase64url(credential.rawId), type: credential.type, response, clientExtensionResults: credential.getClientExtensionResults?.() ?? {}, ...(credential.authenticatorAttachment ? { authenticatorAttachment: credential.authenticatorAttachment } : {}) }
}

const captcha = (input: Captcha) => input.captchaToken ? { gotrue_meta_security: { captcha_token: input.captchaToken } } : {}

function parseSession(raw: string | null): Session | null {
  if (!raw) return null
  try {
    const value = JSON.parse(raw)
    return typeof value?.accessToken === 'string' && typeof value.refreshToken === 'string' && typeof value.expiresAt === 'number' ? Object.freeze(value) : null
  } catch {
    return null
  }
}

export function createAuthClient(options: AuthClientOptions) {
  const origin = baseURL(options.url)
  const fetchImpl = options.fetch ?? globalThis.fetch.bind(globalThis)
  const store = options.store === null ? memoryStore() : options.store ?? defaultStore()
  const margin = options.refreshMarginSeconds ?? 60
  const listeners = new Set<(event: AuthEvent, session: Session | null) => void>()
  const webauthn = () => {
    const credentials = options.credentials ?? (globalThis as { navigator?: Navigator }).navigator?.credentials
    if (!credentials) throw new AuthError(400, 'passkeys_unsupported')
    return credentials
  }
  let refreshing: Promise<Session | null> | null = null

  async function call<T>(method: string, path: string, init: { body?: unknown; token?: string } = {}): Promise<T> {
    const headers: Record<string, string> = {}
    if (init.body !== undefined) headers['Content-Type'] = 'application/json'
    if (init.token) headers.Authorization = `Bearer ${init.token}`
    const response = await fetchImpl(origin + path, { method, headers, body: init.body === undefined ? undefined : JSON.stringify(init.body) })
    const text = await response.text()
    let data: any = null
    try { data = text ? JSON.parse(text) : null } catch { data = null }
    if (!response.ok) {
      // GoTrue reports a hook's refusal (sign-up restrictions) as msg, with
      // error_code "unknown".
      const hookCode = typeof data?.msg === 'string' && /^[a-z0-9_]{1,64}$/.test(data.msg) ? data.msg : undefined
      const errorCode = data?.error_code === 'unknown' ? undefined : data?.error_code
      const code = errorCode ?? hookCode ?? data?.code ?? data?.error
      throw new AuthError(response.status, typeof code === 'string' && /^[a-z0-9_]{1,64}$/.test(code) ? code : `http_${response.status}`)
    }
    return data as T
  }

  function emit(event: AuthEvent, session: Session | null) {
    for (const listener of listeners) {
      try { listener(event, session) } catch { /* listeners never break auth */ }
    }
  }

  async function save(session: Session, event: AuthEvent): Promise<Session> {
    await store.set(JSON.stringify(session))
    emit(event, session)
    return session
  }

  async function clear() {
    await store.remove()
    emit('SIGNED_OUT', null)
  }

  // One refresh at a time: refresh tokens rotate, so concurrent refreshes
  // with the same token would revoke the session.
  function refresh(): Promise<Session | null> {
    refreshing ??= (async () => {
      const current = parseSession(await store.get())
      if (!current) return null
      try {
        return await save(toSession(await call<TokenResponse>('POST', '/auth/v1/token?grant_type=refresh_token', { body: { refresh_token: current.refreshToken } })), 'TOKEN_REFRESHED')
      } catch (err) {
        if (err instanceof AuthError && err.status >= 400 && err.status < 500) await clear()
        throw err
      }
    })().finally(() => { refreshing = null })
    return refreshing
  }

  async function getSession(): Promise<Session | null> {
    const session = parseSession(await store.get())
    if (!session) return null
    return session.expiresAt - margin <= Date.now() / 1000 ? refresh() : session
  }

  async function bearer(): Promise<string> {
    const session = await getSession()
    if (!session) throw new AuthError(401, 'not_authenticated')
    return session.accessToken
  }

  async function authz<T>(method: string, path: string, body?: unknown): Promise<T> {
    return call<T>(method, `/authz/v1${path}`, { body, token: await bearer() })
  }

  const withRedirect = (path: string, redirectTo?: string) => redirectTo ? `${path}${path.includes('?') ? '&' : '?'}redirect_to=${encodeURIComponent(redirectTo)}` : path
  const id = (value: string) => encodeURIComponent(value)
  const domains = {
    list: async (org: string) => (await authz<{ domains: OrgDomain[] }>('GET', `/orgs/${id(org)}/domains`)).domains,
    /** Returns the TXT record to publish; then call verify. */
    add: (org: string, domain: string) => authz<OrgDomain>('POST', `/orgs/${id(org)}/domains`, { domain }),
    verify: (org: string, domain: string) => authz<OrgDomain>('POST', `/orgs/${id(org)}/domains/${id(domain)}/verify`, {}),
    remove: (org: string, domain: string) => authz<null>('DELETE', `/orgs/${id(org)}/domains/${id(domain)}`),
    /**
     * Lets people with a confirmed address at a verified domain join:
     * 'suggest' offers the organization, 'automatic' adds them (and those
     * already there) with role, never owner. A person who left or was removed
     * comes back only by invitation.
     */
    setJoin: (org: string, domain: string, input: { join: DomainJoinMode; role?: string }) =>
      authz<{ domain: string; join: { mode: DomainJoinMode; role: string }; joined: number }>('PATCH', `/orgs/${id(org)}/domains/${id(domain)}`, input),
  }

  return {
    async signUp(input: { email: string; password: string; redirectTo?: string } & Captcha): Promise<{ session: Session | null; user: User | null }> {
      const body = await call<TokenResponse & User>('POST', withRedirect('/auth/v1/signup', input.redirectTo), { body: { email: input.email, password: input.password, ...captcha(input) } })
      if (body?.access_token) {
        const session = await save(toSession(body), 'SIGNED_IN')
        return { session, user: session.user ?? null }
      }
      // Email confirmation is pending; GoTrue returns the user only.
      return { session: null, user: body ?? null }
    },
    /**
     * Starts a session for an anonymous user, when the Auth app allows them.
     * The user becomes permanent, keeping its ID and data, by adding an email
     * address and password with updateUser or a provider with linkIdentity.
     */
    async signInAnonymously(input: Captcha = {}): Promise<Session> {
      return save(toSession(await call<TokenResponse>('POST', '/auth/v1/signup', { body: captcha(input) })), 'SIGNED_IN')
    },
    /**
     * Signs in with a crypto wallet when the Auth app has Web3 sign-in on
     * for that chain. The wallet (window.ethereum or window.solana unless
     * given) asks its owner to sign a message naming this page; the first
     * sign-in creates the user. uri defaults to this page's origin, which must
     * be the app's site URL or an allowed redirect URL.
     */
    async signInWithWeb3(input: { chain: Web3Chain; wallet?: EthereumWallet | SolanaWallet; statement?: string; uri?: string }): Promise<Session> {
      const page = (globalThis as { location?: Location }).location
      const uri = input.uri ?? page?.origin
      if (!uri) throw new AuthError(400, 'web3_uri_required')
      const domain = new URL(uri).host
      const nonce = hexOf(crypto.getRandomValues(new Uint8Array(16)))
      const issuedAt = new Date().toISOString()
      const statement = input.statement ?? `Sign in to ${domain}`
      let message: string, signature: string
      if (input.chain === 'ethereum') {
        const wallet = (input.wallet ?? (globalThis as { ethereum?: EthereumWallet }).ethereum) as EthereumWallet | undefined
        if (!wallet?.request) throw new AuthError(400, 'web3_wallet_not_found')
        const [address] = await wallet.request({ method: 'eth_requestAccounts' }) as string[]
        const chainId = Number.parseInt(String(await wallet.request({ method: 'eth_chainId' })), 16)
        message = web3Message('ethereum', { domain, address, uri, statement, chainId, nonce, issuedAt })
        signature = String(await wallet.request({ method: 'personal_sign', params: [`0x${hexOf(new TextEncoder().encode(message))}`, address] }))
      } else if (input.chain === 'solana') {
        const wallet = (input.wallet ?? (globalThis as { solana?: SolanaWallet }).solana) as SolanaWallet | undefined
        if (!wallet?.signMessage) throw new AuthError(400, 'web3_wallet_not_found')
        if (!wallet.publicKey) await wallet.connect()
        const address = wallet.publicKey?.toBase58()
        if (!address) throw new AuthError(400, 'web3_wallet_not_connected')
        message = web3Message('solana', { domain, address, uri, statement, nonce, issuedAt })
        const signed = await wallet.signMessage(new TextEncoder().encode(message), 'utf8')
        signature = base64Of(signed instanceof Uint8Array ? signed : signed.signature)
      } else {
        throw new AuthError(400, 'web3_unsupported_chain')
      }
      return save(toSession(await call<TokenResponse>('POST', '/auth/v1/token?grant_type=web3', { body: { chain: input.chain, message, signature } })), 'SIGNED_IN')
    },
    async signInWithPassword(input: { email: string; password: string } & Captcha): Promise<Session> {
      return save(toSession(await call<TokenResponse>('POST', '/auth/v1/token?grant_type=password', { body: { email: input.email, password: input.password, ...captcha(input) } })), 'SIGNED_IN')
    },
    async signInWithMagicLink(input: { email: string; redirectTo?: string; createUser?: boolean } & Captcha): Promise<void> {
      await call('POST', withRedirect('/auth/v1/otp', input.redirectTo), { body: { email: input.email, create_user: input.createUser ?? false, ...captcha(input) } })
    },
    /**
     * Texts a one-time code to phone, with its country code (+447700900123).
     * createUser (default false) lets a new number sign up.
     */
    async signInWithPhone(input: { phone: string; createUser?: boolean } & Captcha): Promise<void> {
      await call('POST', '/auth/v1/otp', { body: { phone: input.phone, channel: 'sms', create_user: input.createUser ?? false, ...captcha(input) } })
    },
    /** Completes a phone sign-in with the texted code. */
    async verifyPhone(input: { phone: string; code: string }): Promise<Session> {
      return save(toSession(await call<TokenResponse>('POST', '/auth/v1/verify', { body: { type: 'sms', phone: input.phone, token: input.code } })), 'SIGNED_IN')
    },
    /**
     * Starts organization single sign-on (SAML) for the identity provider of
     * an email domain, or a provider ID. Navigate the browser to the returned
     * URL; the Auth app returns to redirectTo with the session in the URL
     * fragment, which sessionFromUrl() reads.
     */
    async signInWithSSO(input: ({ domain: string } | { providerId: string }) & { redirectTo?: string } & Captcha): Promise<{ url: string }> {
      const target = 'domain' in input ? { domain: input.domain.trim().toLowerCase() } : { provider_id: input.providerId }
      return call<{ url: string }>('POST', '/auth/v1/sso', { body: { ...target, ...(input.redirectTo ? { redirect_to: input.redirectTo } : {}), skip_http_redirect: true, ...captcha(input) } })
    },
    /** Signs in with a passkey; the browser asks the user to choose one. */
    async signInWithPasskey(input: Captcha = {}): Promise<Session> {
      const { challenge_id, options } = await call<{ challenge_id: string; options: CredentialOptionsJSON }>('POST', '/auth/v1/passkeys/authentication/options', { body: captcha(input) })
      const credential = await webauthn().get({ publicKey: decodeOptions(options) })
      if (!credential) throw new AuthError(400, 'passkey_cancelled')
      return save(toSession(await call<TokenResponse>('POST', '/auth/v1/passkeys/authentication/verify', { body: { challenge_id, credential: credentialJSON(credential) } })), 'SIGNED_IN')
    },
    /**
     * Second factors. A user who enrolled one signs in at aal1 and reaches
     * aal2 by verifying a challenge (or a recovery code); require aal2 where
     * it matters, for example with `gregale_auth.claims()->>'aal'` in RLS.
     */
    mfa: {
      listFactors: async (): Promise<Factor[]> => (await call<{ factors?: Factor[] }>('GET', '/auth/v1/user', { token: await bearer() })).factors ?? [],
      /**
       * Starts enrolling an authenticator app; show totp.qr_code (an image
       * URL) or totp.secret, then challenge and verify to activate it.
       */
      enrollTOTP: async (input: { friendlyName?: string; issuer?: string } = {}): Promise<TOTPEnrollment> => {
        const enrollment = await call<TOTPEnrollment>('POST', '/auth/v1/factors', { token: await bearer(), body: { factor_type: 'totp', ...(input.friendlyName ? { friendly_name: input.friendlyName } : {}), ...(input.issuer ? { issuer: input.issuer } : {}) } })
        // GoTrue v2.197.0 returns the QR code as SVG markup.
        const qr = enrollment.totp.qr_code
        return { ...enrollment, totp: { ...enrollment.totp, qr_code: qr.startsWith('data:') ? qr : `data:image/svg+xml;charset=utf-8,${encodeURIComponent(qr)}` } }
      },
      /** Starts enrolling a phone for SMS codes; challenge and verify to activate it. */
      enrollPhone: async (input: { phone: string; friendlyName?: string }): Promise<Factor> =>
        call<Factor>('POST', '/auth/v1/factors', { token: await bearer(), body: { factor_type: 'phone', phone: input.phone, ...(input.friendlyName ? { friendly_name: input.friendlyName } : {}) } }),
      /** Creates a challenge; for a phone factor this texts the code. */
      challenge: async (factorId: string): Promise<FactorChallenge> => call<FactorChallenge>('POST', `/auth/v1/factors/${id(factorId)}/challenge`, { token: await bearer(), body: {} }),
      /** Verifies a challenge; the stored session becomes aal2. */
      verify: async (input: { factorId: string; challengeId: string; code: string }): Promise<Session> =>
        save(toSession(await call<TokenResponse>('POST', `/auth/v1/factors/${id(input.factorId)}/verify`, { token: await bearer(), body: { challenge_id: input.challengeId, code: input.code } })), 'MFA_VERIFIED'),
      unenroll: async (factorId: string): Promise<void> => { await call('DELETE', `/auth/v1/factors/${id(factorId)}`, { token: await bearer() }) },
      /** current is the session's level; next is aal2 when the user has a verified second factor. */
      assuranceLevel: async (): Promise<AssuranceLevel> => {
        const session = await getSession()
        if (!session) return { current: null, next: null }
        const factors = (await call<{ factors?: Factor[] }>('GET', '/auth/v1/user', { token: session.accessToken })).factors ?? []
        const current = aal(session.accessToken)
        return { current, next: factors.some(f => f.status === 'verified' && f.factor_type !== 'recovery_code') ? 'aal2' : current }
      },
      recoveryCodes: {
        /** Creates one-time recovery codes (needs aal2); the codes are returned only here. */
        generate: async (friendlyName?: string): Promise<RecoveryCodes> => call<RecoveryCodes>('POST', '/auth/v1/factors/recovery-codes', { token: await bearer(), body: friendlyName ? { friendly_name: friendlyName } : {} }),
        regenerate: async (): Promise<RecoveryCodes> => call<RecoveryCodes>('POST', '/auth/v1/factors/recovery-codes/regenerate', { token: await bearer(), body: {} }),
        status: async (): Promise<RecoveryCodes> => call<RecoveryCodes>('GET', '/auth/v1/factors/recovery-codes', { token: await bearer() }),
        remove: async (): Promise<void> => { await call('DELETE', '/auth/v1/factors/recovery-codes', { token: await bearer() }) },
        /** Uses one recovery code in place of a second factor; the session becomes aal2. */
        verify: async (code: string): Promise<Session> =>
          save(toSession(await call<TokenResponse>('POST', '/auth/v1/factors/recovery-codes/verify', { token: await bearer(), body: { code } })), 'MFA_VERIFIED'),
      },
    },
    passkeys: {
      /** Registers a new passkey for the signed-in user. */
      register: async (): Promise<void> => {
        const token = await bearer()
        const { challenge_id, options } = await call<{ challenge_id: string; options: CredentialOptionsJSON }>('POST', '/auth/v1/passkeys/registration/options', { token, body: {} })
        const credential = await webauthn().create({ publicKey: decodeOptions(options) })
        if (!credential) throw new AuthError(400, 'passkey_cancelled')
        await call('POST', '/auth/v1/passkeys/registration/verify', { token, body: { challenge_id, credential: credentialJSON(credential) } })
      },
      list: async (): Promise<Passkey[]> => (await call<Passkey[] | null>('GET', '/auth/v1/passkeys', { token: await bearer() })) ?? [],
      rename: async (passkey: string, name: string): Promise<void> => { await call('PATCH', `/auth/v1/passkeys/${id(passkey)}`, { token: await bearer(), body: { friendly_name: name } }) },
      remove: async (passkey: string): Promise<void> => { await call('DELETE', `/auth/v1/passkeys/${id(passkey)}`, { token: await bearer() }) },
    },
    /**
     * Apps the user allowed through the Auth app's OAuth server. Revoking one
     * ends its sessions; it must ask the user again.
     */
    connectedApps: {
      list: async (): Promise<ConnectedApp[]> => (await call<ConnectedApp[] | null>('GET', '/auth/v1/user/oauth/grants', { token: await bearer() })) ?? [],
      revoke: async (clientId: string): Promise<void> => { await call('DELETE', `/auth/v1/user/oauth/grants?client_id=${id(clientId)}`, { token: await bearer() }) },
    },
    /**
     * Links an OAuth provider account to the signed-in user, when the Auth app
     * allows identity linking. Navigate the browser to the returned URL; the
     * Auth app returns to redirectTo with the session in the URL fragment.
     */
    async linkIdentity(input: { provider: string; redirectTo?: string }): Promise<{ url: string }> {
      const query = new URLSearchParams({ provider: input.provider, skip_http_redirect: 'true', ...(input.redirectTo ? { redirect_to: input.redirectTo } : {}) })
      return call<{ url: string }>('GET', `/auth/v1/user/identities/authorize?${query}`, { token: await bearer() })
    },
    /** Removes a sign-in method from the signed-in user; one must remain. */
    async unlinkIdentity(identityId: string): Promise<void> {
      await call('DELETE', `/auth/v1/user/identities/${id(identityId)}`, { token: await bearer() })
    },
    /** Navigate the browser here to sign in with an OAuth provider. */
    oauthUrl(input: { provider: string; redirectTo: string }): string {
      return `${origin}/auth/v1/authorize?provider=${encodeURIComponent(input.provider)}&redirect_to=${encodeURIComponent(input.redirectTo)}`
    },
    /** The hosted sign-in page, which returns to redirectTo with a session. */
    hostedSignInUrl(input: { redirectTo?: string } = {}): string {
      return withRedirect(`${origin}/ui/sign-in`, input.redirectTo)
    },
    /**
     * The hosted account page, where people change their email address and
     * password, second factors, passkeys, sign-in methods, connected apps and
     * sessions. It signs in on its own; Back returns to redirectTo.
     */
    hostedAccountUrl(input: { redirectTo?: string } = {}): string {
      return withRedirect(`${origin}/ui/account`, input.redirectTo)
    },
    /**
     * Completes a redirect from the hosted pages, a magic link or OAuth: reads
     * the session from the URL fragment, stores it and removes it from the
     * address bar. Returns null when the URL carries no session.
     */
    async sessionFromUrl(href: string = (globalThis as { location?: Location }).location?.href ?? ''): Promise<Session | null> {
      const hash = href.includes('#') ? href.slice(href.indexOf('#') + 1) : ''
      const params = new URLSearchParams(hash)
      const accessToken = params.get('access_token')
      const refreshToken = params.get('refresh_token')
      if (!accessToken || !refreshToken) return null
      const session = await save(toSession({ access_token: accessToken, refresh_token: refreshToken, expires_in: Number(params.get('expires_in')), expires_at: Number(params.get('expires_at')) || undefined }), 'SIGNED_IN')
      const history = (globalThis as { history?: History }).history
      if (history && href === (globalThis as { location?: Location }).location?.href) history.replaceState(null, '', href.slice(0, href.indexOf('#')))
      return session
    },
    getSession,
    refresh,
    /** For other clients, e.g. `createDataClient({ accessToken: auth.getAccessToken })`. */
    async getAccessToken(): Promise<string> {
      return bearer()
    },
    async getUser(): Promise<User> {
      return call<User>('GET', '/auth/v1/user', { token: await bearer() })
    },
    async updateUser(input: { email?: string; password?: string; data?: Record<string, unknown> }): Promise<User> {
      return call<User>('PUT', '/auth/v1/user', { token: await bearer(), body: input })
    },
    async resetPasswordForEmail(email: string, input: { redirectTo?: string } & Captcha = {}): Promise<void> {
      await call('POST', withRedirect('/auth/v1/recover', input.redirectTo ?? `${origin}/ui/reset-password`), { body: { email, ...captcha(input) } })
    },
    /**
     * Ends this session, every session of the user with scope "global", or
     * every session but this one with scope "others".
     */
    async signOut(input: { scope?: 'local' | 'global' | 'others' } = {}): Promise<void> {
      const session = parseSession(await store.get())
      try {
        if (session) await call('POST', `/auth/v1/logout?scope=${input.scope ?? 'local'}`, { token: session.accessToken })
      } catch (err) {
        if (!(err instanceof AuthError) || err.status >= 500) throw err
      } finally {
        if (input.scope !== 'others') await clear()
      }
    },
    onAuthStateChange(listener: (event: AuthEvent, session: Session | null) => void): () => void {
      listeners.add(listener)
      return () => { listeners.delete(listener) }
    },
    /**
     * The signed-in user's own sessions, most recently active first. Ending
     * one signs that device out; end this one with signOut().
     */
    sessions: {
      list: async () => (await authz<{ sessions: UserSession[] }>('GET', '/sessions')).sessions,
      revoke: (session: string) => authz<null>('DELETE', `/sessions/${id(session)}`),
    },
    /**
     * Deletes the signed-in user's account, when the Auth app allows it. The
     * session must come from a sign-in in the last ten minutes (with the
     * second step, if the user has one), else this fails with
     * reauthentication_required; the last owner of an organization with
     * other members gets last_owner. Clears the stored session.
     */
    async deleteAccount(): Promise<{ organizations_deleted: number }> {
      const result = await authz<{ deleted: boolean; organizations_deleted: number }>('DELETE', '/account')
      await clear()
      return { organizations_deleted: result.organizations_deleted }
    },
    /**
     * Who is using this session as the user, when the app's support staff
     * opened it (impersonation), such as "admin:0A1B2C3D"; null otherwise.
     * Show a banner, and leave actions the person must take to them.
     */
    async actor(): Promise<string | null> {
      const session = await getSession()
      const act = session ? claims(session.accessToken).act as { sub?: unknown } | undefined : undefined
      return typeof act?.sub === 'string' ? act.sub : null
    },
    organizations: {
      list: async () => (await authz<{ organizations: Organization[] }>('GET', '/orgs')).organizations,
      create: (input: { slug: string; name: string }) => authz<Organization>('POST', '/orgs', input),
      /**
       * The organization's audit log, newest first (needs audit:read; owners
       * hold it). Pass the returned next as cursor for the following page.
       */
      auditLog: (org: string, input: { cursor?: string; limit?: number } = {}) => {
        const query = new URLSearchParams({ ...(input.cursor ? { cursor: input.cursor } : {}), ...(input.limit ? { limit: String(input.limit) } : {}) }).toString()
        return authz<{ entries: AuditEntry[]; next: string | null }>('GET', `/orgs/${id(org)}/audit-log${query ? `?${query}` : ''}`)
      },
      /**
       * Requires (or stops requiring) a verified second factor for the
       * organization's permissions. Needs mfa:manage (owners hold it) and a
       * session at aal2 either way; others get forbidden or mfa_required.
       */
      setMfaRequired: (org: string, required: boolean) =>
        authz<Organization>('PATCH', `/orgs/${id(org)}`, { mfa_required: required }),
      /**
       * Makes org (one the user belongs to) the session's active
       * organization, or none with null, and refreshes the session so its
       * token carries it. Needs organization claims on in the Auth app. An
       * organization that requires a second factor fails with mfa_required
       * until the session is aal2.
       */
      setActive: async (org: string | null): Promise<ActiveOrganization | null> => {
        await authz('POST', '/active-organization', { org })
        const session = await refresh()
        return session ? activeOrganization(session.accessToken) : null
      },
      /** The session's active organization, read from its token; null when none. */
      active: async (): Promise<ActiveOrganization | null> => {
        const session = await getSession()
        return session ? activeOrganization(session.accessToken) : null
      },
      /**
       * The organization the user may join because it verified the user's
       * confirmed email domain (organization domains on in the Auth app).
       */
      suggested: async () => (await authz<{ organizations: SuggestedOrganization[] }>('GET', '/suggested-organizations')).organizations,
      /** Joins a suggested organization with the role its domain gives. */
      join: (org: string) => authz<{ org_id: string; role: string }>('POST', `/orgs/${id(org)}/join`, {}),
      /**
       * Email domains an organization proves with a DNS TXT record (owners,
       * or members with domains:manage or sso:manage), and whether people at
       * each join it.
       */
      domains,
    },
    members: {
      list: async (org: string) => (await authz<{ members: Member[] }>('GET', `/orgs/${id(org)}/members`)).members,
      setRole: (org: string, user: string, role: string) => authz<{ org_id: string; user_id: string; role: string }>('PATCH', `/orgs/${id(org)}/members/${id(user)}`, { role }),
      remove: (org: string, user: string) => authz<null>('DELETE', `/orgs/${id(org)}/members/${id(user)}`),
    },
    invitations: {
      create: (org: string, input: { email: string; role: string; expiresIn?: number }) =>
        authz<CreatedInvitation>('POST', `/orgs/${id(org)}/invitations`, { email: input.email, role: input.role, ...(input.expiresIn ? { expires_in: input.expiresIn } : {}) }),
      list: async (org: string) => (await authz<{ invitations: Invitation[] }>('GET', `/orgs/${id(org)}/invitations`)).invitations,
      revoke: (org: string, invitation: string) => authz<null>('DELETE', `/orgs/${id(org)}/invitations/${id(invitation)}`),
      /** mfa_required: the organization needs a verified second factor before the new member can use it. */
      accept: (token: string) => authz<{ org_id: string; role: string; mfa_required?: boolean }>('POST', '/invitations/accept', { token }),
    },
    /**
     * Self-service organization SSO, when the app owner turned it on: an
     * organization's owners (or members with sso:manage) verify email
     * domains with a DNS TXT record, then connect their own SAML identity
     * provider for those domains.
     */
    orgSSO: {
      domains,
      providers: {
        list: async (org: string) => (await authz<{ providers: OrgSSOProvider[] }>('GET', `/orgs/${id(org)}/sso`)).providers,
        add: (org: string, input: { metadataUrl?: string; metadataXml?: string; domains: string[]; role?: string }) =>
          authz<OrgSSOProvider>('POST', `/orgs/${id(org)}/sso`, ssoBody(input)),
        update: (org: string, provider: string, input: { metadataUrl?: string; metadataXml?: string; domains?: string[]; role?: string }) =>
          authz<OrgSSOProvider>('PATCH', `/orgs/${id(org)}/sso/${id(provider)}`, ssoBody(input)),
        remove: (org: string, provider: string) => authz<null>('DELETE', `/orgs/${id(org)}/sso/${id(provider)}`),
        /** Creates the token the identity provider uses for SCIM provisioning; shown once. */
        scimToken: (org: string, provider: string) => authz<{ token: string; scim_url: string }>('POST', `/orgs/${id(org)}/sso/${id(provider)}/scim-token`, {}),
      },
    },
    /** Asks the Auth app whether the signed-in user may take action on resource. */
    async authorize(action: string, resource: string): Promise<boolean> {
      return (await authz<{ allowed: boolean }>('POST', '/authorize', { action, resource })).allowed === true
    },
  }
}

export type AuthClient = ReturnType<typeof createAuthClient>

function ssoBody(input: { metadataUrl?: string; metadataXml?: string; domains?: string[]; role?: string }) {
  return {
    ...(input.metadataUrl ? { metadata_url: input.metadataUrl } : {}), ...(input.metadataXml ? { metadata_xml: input.metadataXml } : {}),
    ...(input.domains ? { domains: input.domains } : {}), ...(input.role ? { role: input.role } : {}),
  }
}

/** The resource name authorize() and gregale_auth.authorize() expect. */
export function resource(org: string, kind: string, name: string | number): string {
  return `org/${org}/${kind}/${name}`
}
