// React bindings for @gregale/auth (`@gregale/auth/react`, ADR-1190).
//
//   const auth = createAuthClient({ url: 'https://shop-auth.gregale.app' })
//
//   <AuthProvider client={auth}>
//     <SignedOut><SignIn providers={['google']} magicLink /></SignedOut>
//     <SignedIn><OrganizationSwitcher /><UserMenu /><App /></SignedIn>
//   </AuthProvider>
//
// The provider reads the stored session, completes a redirect from the hosted
// pages, a magic link or OAuth, and re-renders on sign-in, refresh and
// sign-out. A user with a verified second factor is not signed in until they
// complete it; <SignIn> asks for the code. Components render plain HTML with
// gregale-auth-* class names to style. React 18 or later is required.

import { createContext, createElement as h, Fragment, useCallback, useContext, useEffect, useMemo, useState, useSyncExternalStore, type ReactElement, type ReactNode } from 'react'

import { AuthError, type AuthClient, type Factor, type Organization, type Session, type User } from './index.js'
import { aal, activeOrganization, claims, type ActiveOrganization } from './claims.js'

/** signed-out also covers a sign-in waiting for its second step. */
export type AuthStatus = 'loading' | 'signed-out' | 'second-step' | 'signed-in'

export type AuthState = Readonly<{
  status: AuthStatus
  /** The stored session; during the second step it is the aal1 session. */
  session: Session | null
  /** The session's user, from the sign-in response or its access token. */
  user: User | null
  /** The session's active organization, from its token's org_* claims. */
  organization: ActiveOrganization | null
}>

type Store = {
  client: AuthClient
  get(): AuthState
  subscribe(listener: () => void): () => void
  start(): () => void
}

const AuthContext = createContext<Store | null>(null)

const loading: AuthState = Object.freeze({ status: 'loading', session: null, user: null, organization: null })

// A token's claims stand in for the user when the session came from a URL
// fragment, which carries no user object.
function sessionUser(session: Session): User {
  if (session.user) return session.user
  const c = claims(session.accessToken)
  const user: Record<string, unknown> = { id: typeof c.sub === 'string' ? c.sub : '' }
  for (const key of ['email', 'phone', 'role', 'is_anonymous', 'app_metadata', 'user_metadata']) {
    if (c[key] !== undefined && c[key] !== '') user[key] = c[key]
  }
  return user as User
}

// The factors a sign-in can be completed with, as on the hosted pages.
function secondFactors(factors: readonly Factor[]): { factors: Factor[]; recovery: boolean } {
  const verified = factors.filter(factor => factor.status === 'verified')
  return { factors: verified.filter(factor => factor.factor_type === 'totp' || factor.factor_type === 'phone'), recovery: verified.some(factor => factor.factor_type === 'recovery_code') }
}

// The check only decides what to show: the Auth app and the application's
// own rules enforce aal2 where it matters, so a failed check shows the
// application rather than locking the user out.
async function statusOf(client: AuthClient, session: Session | null, secondStep: boolean): Promise<AuthStatus> {
  if (!session) return 'signed-out'
  if (!secondStep || aal(session.accessToken) === 'aal2') return 'signed-in'
  try {
    return secondFactors(await client.mfa.listFactors()).factors.length ? 'second-step' : 'signed-in'
  } catch {
    return 'signed-in'
  }
}

function createStore(client: AuthClient, options: { handleRedirect: boolean; secondStep: boolean }): Store {
  let state = loading
  let generation = 0
  const listeners = new Set<() => void>()
  // Each change supersedes a check still running for an older one.
  async function update(session: Session | null) {
    const mine = ++generation
    const status = await statusOf(client, session, options.secondStep)
    if (mine !== generation) return
    state = Object.freeze({ status, session, user: session ? sessionUser(session) : null, organization: session ? activeOrganization(session.accessToken) : null })
    for (const listener of listeners) listener()
  }
  return {
    client,
    get: () => state,
    subscribe(listener) {
      listeners.add(listener)
      return () => { listeners.delete(listener) }
    },
    start() {
      const off = client.onAuthStateChange((_event, session) => { void update(session) })
      const first = generation
      void (async () => {
        let session: Session | null = null
        try {
          session = (options.handleRedirect ? await client.sessionFromUrl() : null) ?? await client.getSession()
        } catch { /* an unusable stored session reads as signed out */ }
        // An auth event while loading has already published a newer state.
        if (first === generation) await update(session)
      })()
      return () => { generation++; off() }
    },
  }
}

export type AuthProviderProps = {
  client: AuthClient
  /** Completes a session in the URL fragment on mount (default true). */
  handleRedirect?: boolean
  /**
   * Treats a user with a verified authenticator app or phone as signed in
   * only after the second step (default true). Checking costs one request
   * per aal1 session.
   */
  requireSecondStep?: boolean
  children?: ReactNode
}

/** Makes an Auth client and its session available to the hooks and components below it. */
export function AuthProvider({ client, handleRedirect = true, requireSecondStep = true, children }: AuthProviderProps): ReactElement {
  // Options apply when the client is first provided, so they are not dependencies.
  const store = useMemo(() => createStore(client, { handleRedirect, secondStep: requireSecondStep }), [client])
  useEffect(() => store.start(), [store])
  return h(AuthContext.Provider, { value: store }, children)
}

function useStore(): Store {
  const store = useContext(AuthContext)
  if (!store) throw new Error('@gregale/auth/react needs an <AuthProvider client={...}> above it')
  return store
}

/** The client from the nearest AuthProvider. */
export function useAuthClient(): AuthClient {
  return useStore().client
}

/** The sign-in status, session, user and active organization; re-renders when they change. */
export function useAuth(): AuthState {
  const store = useStore()
  return useSyncExternalStore(store.subscribe, store.get, store.get)
}

/** The session once signed in, else null. */
export function useSession(): Session | null {
  const auth = useAuth()
  return auth.status === 'signed-in' ? auth.session : null
}

/** The signed-in user, else null. */
export function useUser(): User | null {
  const auth = useAuth()
  return auth.status === 'signed-in' ? auth.user : null
}

/** The session's active organization (organization claims must be on in the Auth app), else null. */
export function useActiveOrganization(): ActiveOrganization | null {
  const auth = useAuth()
  return auth.status === 'signed-in' ? auth.organization : null
}

/**
 * Whether the signed-in user may do action on resource, asked of the Auth
 * app; undefined while loading or asking, false when signed out. Asked again
 * when the session's token changes, for example on switching organization.
 * Hide what users cannot do with it; enforce in your backend.
 */
export function useAuthorized(action: string, resource: string): boolean | undefined {
  const client = useAuthClient()
  const { status, session } = useAuth()
  const key = status === 'signed-in' && session ? `${session.accessToken}\n${action}\n${resource}` : ''
  const [answer, setAnswer] = useState<{ key: string; allowed: boolean }>()
  useEffect(() => {
    if (!key) return
    let current = true
    client.authorize(action, resource).catch(() => false).then(allowed => { if (current) setAnswer({ key, allowed }) })
    return () => { current = false }
  }, [client, key])
  if (status === 'loading') return undefined
  if (!key) return false
  return answer?.key === key ? answer.allowed : undefined
}

type ChildrenProps = { children?: ReactNode }

/** Renders its children once the user is signed in, after any second step. */
export function SignedIn({ children }: ChildrenProps): ReactElement | null {
  return useAuth().status === 'signed-in' ? h(Fragment, null, children) : null
}

/** Renders its children when nobody is signed in or a second step is pending; nothing while loading. */
export function SignedOut({ children }: ChildrenProps): ReactElement | null {
  const { status } = useAuth()
  return status === 'signed-out' || status === 'second-step' ? h(Fragment, null, children) : null
}

/** Renders its children when useAuthorized(action, resource) is true, else fallback once known. */
export function Authorized({ action, resource, fallback = null, children }: { action: string; resource: string; fallback?: ReactNode; children?: ReactNode }): ReactElement | null {
  const allowed = useAuthorized(action, resource)
  return allowed === undefined ? null : h(Fragment, null, allowed ? children : fallback)
}

const messages: Record<string, string> = {
  invalid_credentials: 'Wrong email or password.',
  email_not_confirmed: 'Confirm your email address first; check your inbox.',
  user_already_exists: 'An account with this email already exists. Sign in instead.',
  weak_password: 'Choose a longer, less common password.',
  over_email_send_rate_limit: 'Too many emails sent. Wait a minute and try again.',
  email_provider_unconfigured: 'This application cannot send email yet.',
  signup_disabled: 'Sign-up is closed for this application.',
  otp_disabled: 'We could not send a sign-in link to this address.',
  webauthn_verification_failed: 'That passkey could not be verified. Try again or use your password.',
  webauthn_challenge_not_found: 'The passkey request expired. Try again.',
  passkey_not_found: 'This passkey is no longer registered. Sign in another way.',
  passkey_cancelled: 'No passkey was chosen.',
  passkeys_unsupported: 'This browser cannot use passkeys.',
  captcha_failed: 'The security check failed or expired. Complete it again.',
  mfa_verification_failed: 'That code is wrong. Try again.',
  mfa_challenge_expired: 'That code has expired. Request a new one.',
  mfa_recovery_codes_locked: 'Too many wrong recovery codes. Wait 15 minutes and try again.',
  over_sms_send_rate_limit: 'Too many codes sent. Wait a minute and try again.',
  reauthentication_required: 'Sign in again to do this; your sign-in is too old.',
}

/** A sentence for an AuthError, as the hosted pages show it; for your own forms. */
export function authErrorMessage(err: unknown): string {
  if (err instanceof AuthError) return messages[err.code] ?? (err.status === 429 ? 'Too many attempts. Wait a minute and try again.' : 'Something went wrong. Try again.')
  return 'Could not reach the sign-in service. Check your connection and try again.'
}

// run does one action at a time and shows its failure; overrides name what a
// code means to this component.
function useAction(overrides: Record<string, string> = {}) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const run = useCallback(async (work: () => Promise<void>) => {
    setBusy(true)
    setError(null)
    try {
      await work()
    } catch (err) {
      setError(err instanceof AuthError && overrides[err.code] ? overrides[err.code] : authErrorMessage(err))
    } finally {
      setBusy(false)
    }
  }, [])
  return { busy, error, run }
}

const cx = (...names: (string | false | undefined)[]) => names.filter(Boolean).join(' ')

const page = () => (globalThis as { location?: { href: string; assign(url: string): void } }).location

/** This page without its fragment: where sign-ins return by default. */
function here(): string | undefined {
  const href = page()?.href
  return href ? href.split('#')[0] : undefined
}

const providerNames: Record<string, string> = { github: 'GitHub', gitlab: 'GitLab', linkedin_oidc: 'LinkedIn', slack_oidc: 'Slack', workos: 'WorkOS', x: 'X', azure: 'Microsoft' }
const providerName = (id: string) => providerNames[id] ?? id.charAt(0).toUpperCase() + id.slice(1)

function notes(error: string | null, notice?: string | null): ReactElement {
  return h(Fragment, null,
    error ? h('p', { className: 'gregale-auth-error', role: 'alert' }, error) : null,
    notice ? h('p', { className: 'gregale-auth-notice', role: 'status' }, notice) : null)
}

function field(label: string, props: Record<string, unknown>): ReactElement {
  return h('label', { className: 'gregale-auth-field' }, label, h('input', props))
}

export type SignInProps = {
  /** OAuth providers to offer, such as ['google', 'github']; each must be on in the Auth app. */
  providers?: readonly string[]
  /** Email and password (default true). */
  password?: boolean
  /** Offer creating an account; a sign-in link may also create one (default true). */
  signUp?: boolean
  /** Offer an emailed sign-in link (default false). */
  magicLink?: boolean
  /** Offer passkeys (default false). */
  passkeys?: boolean
  /**
   * Where OAuth, sign-in links and email confirmation return; defaults to
   * this page. It must be the site URL or an allowed redirect URL.
   */
  redirectTo?: string
  /** The CAPTCHA token when the Auth app requires one; render the widget yourself. */
  captchaToken?: string
  className?: string
}

/**
 * A sign-in form for the methods you list, then the second step for users
 * with one. Renders nothing once signed in or while loading.
 */
export function SignIn(props: SignInProps): ReactElement | null {
  const { status } = useAuth()
  if (status === 'second-step') return h(SecondStep, { className: props.className })
  if (status !== 'signed-out') return null
  return h(FirstStep, props)
}

function FirstStep({ providers = [], password: withPassword = true, signUp = true, magicLink = false, passkeys = false, redirectTo, captchaToken, className }: SignInProps): ReactElement {
  const client = useAuthClient()
  const { busy, error, run } = useAction()
  const [creating, setCreating] = useState(false)
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [notice, setNotice] = useState<string | null>(null)
  const target = () => redirectTo ?? here()
  const captcha = captchaToken ? { captchaToken } : {}
  const link = async () => {
    await client.signInWithMagicLink({ email: email.trim(), redirectTo: target(), createUser: signUp, ...captcha })
    setNotice('Check your email for a sign-in link.')
  }
  const sendLink = () => run(link)
  const submit = () => run(async () => {
    setNotice(null)
    if (!withPassword) return link()
    if (!creating) {
      await client.signInWithPassword({ email: email.trim(), password, ...captcha })
      return
    }
    const { session } = await client.signUp({ email: email.trim(), password, redirectTo: target(), ...captcha })
    if (!session) setNotice('Check your email to confirm your address, then sign in.')
  })
  const oauth = (provider: string) => {
    const to = target()
    if (to) page()?.assign(client.oauthUrl({ provider, redirectTo: to }))
  }
  const passkey = () => run(async () => { await client.signInWithPasskey(captcha) })
  const emailForm = withPassword || magicLink
  return h('form', {
    className: cx('gregale-auth', 'gregale-auth-sign-in', className),
    onSubmit: (event: { preventDefault(): void }) => { event.preventDefault(); void submit() },
  },
  providers.length || passkeys ? h('div', { className: 'gregale-auth-providers' },
    ...providers.map(provider => h('button', { key: provider, type: 'button', disabled: busy, onClick: () => oauth(provider) }, `Continue with ${providerName(provider)}`)),
    passkeys ? h('button', { key: 'passkey', type: 'button', disabled: busy, onClick: () => void passkey() }, 'Sign in with a passkey') : null) : null,
  emailForm ? field('Email', { name: 'email', type: 'email', autoComplete: 'email', required: true, value: email, onChange: (event: { target: { value: string } }) => setEmail(event.target.value) }) : null,
  withPassword ? field('Password', { name: 'password', type: 'password', autoComplete: creating ? 'new-password' : 'current-password', required: true, value: password, onChange: (event: { target: { value: string } }) => setPassword(event.target.value) }) : null,
  emailForm ? h('button', { type: 'submit', disabled: busy }, !withPassword ? 'Email me a sign-in link' : creating ? 'Create account' : 'Sign in') : null,
  withPassword && magicLink && !creating ? h('button', { type: 'button', className: 'gregale-auth-secondary', disabled: busy, onClick: () => void sendLink() }, 'Email me a sign-in link instead') : null,
  withPassword && signUp ? h('button', { type: 'button', className: 'gregale-auth-switch', disabled: busy, onClick: () => { setCreating(!creating); setNotice(null) } }, creating ? 'Have an account? Sign in' : 'New here? Create an account') : null,
  notes(error, notice))
}

type Method = { factor: Factor; challenge: string } | 'recovery'

function SecondStep({ className }: { className?: string }): ReactElement {
  const client = useAuthClient()
  const { busy, error, run } = useAction()
  const [options, setOptions] = useState<{ factors: Factor[]; recovery: boolean } | null>(null)
  const [method, setMethod] = useState<Method | null>(null)
  const [code, setCode] = useState('')
  const [notice, setNotice] = useState<string | null>(null)
  const challenge = async (factor: Factor) => {
    setMethod(null)
    setCode('')
    const { id } = await client.mfa.challenge(factor.id)
    setMethod({ factor, challenge: id })
    setNotice(factor.factor_type === 'phone' ? `We texted a code to ${factor.phone ?? 'your phone'}.` : `Enter the code from ${factor.friendly_name || 'your authenticator app'}.`)
  }
  const use = (factor: Factor) => run(() => challenge(factor))
  useEffect(() => {
    void run(async () => {
      const found = secondFactors(await client.mfa.listFactors())
      setOptions(found)
      if (found.factors[0]) await challenge(found.factors[0])
    })
  }, [client])
  const submit = () => run(async () => {
    if (method === 'recovery') await client.mfa.recoveryCodes.verify(code.trim())
    else if (method) await client.mfa.verify({ factorId: method.factor.id, challengeId: method.challenge, code: code.trim() })
  })
  const others = [
    ...(options?.factors ?? []).filter(factor => method === 'recovery' || factor.id !== method?.factor.id).map(factor => h('button', { key: factor.id, type: 'button', disabled: busy, onClick: () => void use(factor) },
      factor.factor_type === 'phone' ? 'Text me a code instead' : `Use ${factor.friendly_name || 'an authenticator app'} instead`)),
    options?.recovery && method !== 'recovery' ? h('button', { key: 'recovery', type: 'button', disabled: busy, onClick: () => { setMethod('recovery'); setCode(''); setNotice('Enter one of your recovery codes. Each code works once.') } }, 'Use a recovery code') : null,
    h('button', { key: 'cancel', type: 'button', className: 'gregale-auth-switch', disabled: busy, onClick: () => void run(() => client.signOut()) }, 'Use another account'),
  ]
  return h('form', {
    className: cx('gregale-auth', 'gregale-auth-second-step', className),
    onSubmit: (event: { preventDefault(): void }) => { event.preventDefault(); void submit() },
  },
  method ? field(method === 'recovery' ? 'Recovery code' : 'Code', method === 'recovery'
    ? { name: 'code', autoComplete: 'off', spellCheck: false, required: true, value: code, onChange: (event: { target: { value: string } }) => setCode(event.target.value) }
    : { name: 'code', inputMode: 'numeric', autoComplete: 'one-time-code', required: true, value: code, onChange: (event: { target: { value: string } }) => setCode(event.target.value) }) : null,
  method ? h('button', { type: 'submit', disabled: busy }, 'Verify') : null,
  h('div', { className: 'gregale-auth-providers' }, ...others),
  notes(error, notice))
}

type ButtonProps = { children?: ReactNode; className?: string }

/** Sends the user to the hosted sign-in page, which returns to redirectTo (default this page). */
export function SignInButton({ redirectTo, children = 'Sign in', className }: ButtonProps & { redirectTo?: string }): ReactElement {
  const client = useAuthClient()
  return h('button', { type: 'button', className: cx('gregale-auth-sign-in-button', className), onClick: () => page()?.assign(client.hostedSignInUrl({ redirectTo: redirectTo ?? here() })) }, children)
}

/**
 * Signs out this session, or every session of the user with scope
 * "global". The stored session is cleared even when the Auth app cannot be
 * reached.
 */
export function SignOutButton({ scope = 'local', children = 'Sign out', className }: ButtonProps & { scope?: 'local' | 'global' }): ReactElement {
  const client = useAuthClient()
  const [busy, setBusy] = useState(false)
  const signOut = async () => {
    setBusy(true)
    try { await client.signOut({ scope }) } catch { /* cleared locally regardless */ } finally { setBusy(false) }
  }
  return h('button', { type: 'button', className: cx('gregale-auth-sign-out-button', className), disabled: busy, onClick: () => void signOut() }, children)
}

/** The signed-in user's email, a link to the hosted account page and sign-out. */
export function UserMenu({ redirectTo, className }: { redirectTo?: string; className?: string }): ReactElement | null {
  const client = useAuthClient()
  const user = useUser()
  if (!user) return null
  const name = typeof user.email === 'string' && user.email ? user.email : typeof user.phone === 'string' && user.phone ? user.phone : user.is_anonymous ? 'Guest' : 'Signed in'
  return h('div', { className: cx('gregale-auth-user-menu', className) },
    h('span', { className: 'gregale-auth-user' }, name),
    h('button', { type: 'button', onClick: () => page()?.assign(client.hostedAccountUrl({ redirectTo: redirectTo ?? here() })) }, 'Account'),
    h(SignOutButton, null))
}

export type OrganizationSwitcherProps = {
  /** Offer acting in no organization (default true). */
  allowNone?: boolean
  noneLabel?: string
  /** Called with the new active organization once the session carries it. */
  onChange?: (organization: ActiveOrganization | null) => void
  className?: string
}

/**
 * Lists the user's organizations and switches the session's active one.
 * Needs organization claims on in the Auth app.
 */
export function OrganizationSwitcher({ allowNone = true, noneLabel = 'Personal account', onChange, className }: OrganizationSwitcherProps): ReactElement | null {
  const client = useAuthClient()
  const { status, organization } = useAuth()
  const { busy, error, run } = useAction({ not_found: 'That organization is not available, or switching is off in this application.' })
  const [organizations, setOrganizations] = useState<Organization[] | null>(null)
  useEffect(() => {
    if (status !== 'signed-in') return
    let current = true
    void run(async () => {
      const list = await client.organizations.list()
      if (current) setOrganizations(list)
    })
    return () => { current = false }
  }, [client, status])
  if (status !== 'signed-in') return null
  const select = (value: string) => run(async () => {
    const active = await client.organizations.setActive(value || null)
    onChange?.(active)
  })
  const active = organization?.id ?? ''
  return h('div', { className: cx('gregale-auth-organizations', className) },
    organizations ? h('select', { 'aria-label': 'Organization', value: active, disabled: busy, onChange: (event: { target: { value: string } }) => void select(event.target.value) },
      allowNone ? h('option', { value: '' }, noneLabel) : active ? null : h('option', { value: '', disabled: true }, 'Choose an organization'),
      ...organizations.map(org => h('option', { key: org.id, value: org.id }, org.name))) : null,
    notes(error))
}
