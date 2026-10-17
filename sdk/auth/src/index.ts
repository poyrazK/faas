// @gregale/auth: client for a Gregale application Auth app (ADR-1190).
// Sign-up, sign-in, passkeys, sessions with single-flight refresh, the hosted
// pages, and the /authz/v1 organization and authorization API. No
// dependencies.

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

export type AuthEvent = 'SIGNED_IN' | 'TOKEN_REFRESHED' | 'SIGNED_OUT'

export type Organization = Readonly<{ id: string; slug: string; name: string; role?: string; created_at: string }>
export type Member = Readonly<{ user_id: string; email: string; role: string; created_at: string }>
export type Invitation = Readonly<{ id: string; email: string; role: string; expires_at: string; created_at?: string }>
export type CreatedInvitation = Invitation & Readonly<{ delivered: boolean; token?: string }>

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

function baseURL(value: string): string {
  const url = new URL(value)
  if (url.username || url.password || url.search || url.hash) throw new Error('Auth URL must not contain credentials, a query or a fragment')
  const loopback = ['localhost', '127.0.0.1', '[::1]'].includes(url.hostname)
  if (url.protocol !== 'https:' && !(url.protocol === 'http:' && loopback)) throw new Error('Auth requires HTTPS')
  return url.origin
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
      const code = data?.error_code ?? data?.code ?? data?.error
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
    /** Navigate the browser here to sign in with an OAuth provider. */
    oauthUrl(input: { provider: string; redirectTo: string }): string {
      return `${origin}/auth/v1/authorize?provider=${encodeURIComponent(input.provider)}&redirect_to=${encodeURIComponent(input.redirectTo)}`
    },
    /** The hosted sign-in page, which returns to redirectTo with a session. */
    hostedSignInUrl(input: { redirectTo?: string } = {}): string {
      return withRedirect(`${origin}/ui/sign-in`, input.redirectTo)
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
    /** Ends this session, or every session of the user with scope "global". */
    async signOut(input: { scope?: 'local' | 'global' } = {}): Promise<void> {
      const session = parseSession(await store.get())
      try {
        if (session) await call('POST', `/auth/v1/logout?scope=${input.scope ?? 'local'}`, { token: session.accessToken })
      } catch (err) {
        if (!(err instanceof AuthError) || err.status >= 500) throw err
      } finally {
        await clear()
      }
    },
    onAuthStateChange(listener: (event: AuthEvent, session: Session | null) => void): () => void {
      listeners.add(listener)
      return () => { listeners.delete(listener) }
    },
    organizations: {
      list: async () => (await authz<{ organizations: Organization[] }>('GET', '/orgs')).organizations,
      create: (input: { slug: string; name: string }) => authz<Organization>('POST', '/orgs', input),
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
      accept: (token: string) => authz<{ org_id: string; role: string }>('POST', '/invitations/accept', { token }),
    },
    /** Asks the Auth app whether the signed-in user may take action on resource. */
    async authorize(action: string, resource: string): Promise<boolean> {
      return (await authz<{ allowed: boolean }>('POST', '/authorize', { action, resource })).allowed === true
    },
  }
}

export type AuthClient = ReturnType<typeof createAuthClient>

/** The resource name authorize() and gregale_auth.authorize() expect. */
export function resource(org: string, kind: string, name: string | number): string {
  return `org/${org}/${kind}/${name}`
}
