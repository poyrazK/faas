// @gregale/auth: client for a Gregale application Auth app (ADR-1190).
// Sign-up, sign-in, sessions with single-flight refresh, the hosted pages,
// and the /authz/v1 organization and authorization API. No dependencies.

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
}

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
    async signUp(input: { email: string; password: string; redirectTo?: string }): Promise<{ session: Session | null; user: User | null }> {
      const body = await call<TokenResponse & User>('POST', withRedirect('/auth/v1/signup', input.redirectTo), { body: { email: input.email, password: input.password } })
      if (body?.access_token) {
        const session = await save(toSession(body), 'SIGNED_IN')
        return { session, user: session.user ?? null }
      }
      // Email confirmation is pending; GoTrue returns the user only.
      return { session: null, user: body ?? null }
    },
    async signInWithPassword(input: { email: string; password: string }): Promise<Session> {
      return save(toSession(await call<TokenResponse>('POST', '/auth/v1/token?grant_type=password', { body: input })), 'SIGNED_IN')
    },
    async signInWithMagicLink(input: { email: string; redirectTo?: string; createUser?: boolean }): Promise<void> {
      await call('POST', withRedirect('/auth/v1/otp', input.redirectTo), { body: { email: input.email, create_user: input.createUser ?? false } })
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
    async resetPasswordForEmail(email: string, input: { redirectTo?: string } = {}): Promise<void> {
      await call('POST', withRedirect('/auth/v1/recover', input.redirectTo ?? `${origin}/ui/reset-password`), { body: { email } })
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
