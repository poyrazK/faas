// Hosted sign-in pages for application Auth (ADR-1190). Plain ES module, no
// dependencies, served under a strict CSP. Sessions are handed to the
// application only in the URL fragment, only to the site URL or an allowed
// redirect URL, and are never written to storage by these pages.

export const routes = ['sign-in', 'sign-up', 'forgot-password', 'reset-password', 'invitation']

// allowedRedirect returns target when it is the site URL or starts with an
// allowed redirect URL (the same list GoTrue enforces), else the site URL.
export function allowedRedirect(target, siteURL, redirectURLs) {
  if (!target) return siteURL
  let url
  try { url = new URL(target) } catch { return siteURL }
  if (url.username || url.password || !['https:', 'http:'].includes(url.protocol)) return siteURL
  const allowed = [siteURL, ...redirectURLs].some(base => {
    try {
      const b = new URL(base)
      return url.origin === b.origin && url.pathname.startsWith(b.pathname)
    } catch { return false }
  })
  return allowed ? url.toString() : siteURL
}

// fragmentParams reads OAuth-style results GoTrue puts in the fragment.
export function fragmentParams(hash) {
  return Object.fromEntries(new URLSearchParams(hash.replace(/^#/, '')))
}

// handoffURL appends the session to the target's fragment, replacing any.
export function handoffURL(target, session) {
  const url = new URL(target)
  const params = new URLSearchParams({
    access_token: session.access_token,
    refresh_token: session.refresh_token,
    expires_in: String(session.expires_in),
    expires_at: String(session.expires_at ?? Math.floor(Date.now() / 1000) + session.expires_in),
    token_type: 'bearer',
    type: 'signin',
  })
  url.hash = params.toString()
  return url.toString()
}

const providerNames = { github: 'GitHub', gitlab: 'GitLab', linkedin_oidc: 'LinkedIn', slack_oidc: 'Slack', workos: 'WorkOS', x: 'X', azure: 'Microsoft' }

export function providerName(id) {
  return providerNames[id] ?? id.charAt(0).toUpperCase() + id.slice(1)
}

export function errorMessage(body, status) {
  const code = body?.error_code ?? body?.code ?? body?.error
  const known = {
    invalid_credentials: 'Wrong email or password.',
    email_not_confirmed: 'Confirm your email address first; check your inbox.',
    user_already_exists: 'An account with this email already exists. Sign in instead.',
    weak_password: 'Choose a longer, less common password.',
    over_email_send_rate_limit: 'Too many emails sent. Wait a minute and try again.',
    email_provider_unconfigured: 'This application cannot send email yet.',
    signup_disabled: 'Sign-up is closed for this application.',
    invitation_invalid: 'This invitation is no longer valid.',
    invitation_email_mismatch: 'This invitation was sent to a different email address.',
    already_member: 'You are already a member.',
  }
  return known[code] ?? (status === 429 ? 'Too many attempts. Wait a minute and try again.' : 'Something went wrong. Try again.')
}

async function request(method, path, { body, token } = {}) {
  const response = await fetch(path, {
    method,
    headers: { ...(body ? { 'Content-Type': 'application/json' } : {}), ...(token ? { Authorization: `Bearer ${token}` } : {}) },
    body: body ? JSON.stringify(body) : undefined,
  })
  const text = await response.text()
  let data = null
  try { data = text ? JSON.parse(text) : null } catch { data = null }
  if (!response.ok) throw Object.assign(new Error(errorMessage(data, response.status)), { status: response.status, data })
  return data
}

// The DOM layer below runs only in a browser.
function el(tag, attrs = {}, ...children) {
  const node = document.createElement(tag)
  for (const [k, v] of Object.entries(attrs)) {
    if (k === 'on') for (const [event, fn] of Object.entries(v)) node.addEventListener(event, fn)
    else node.setAttribute(k, v)
  }
  for (const child of children) node.append(child)
  return node
}

function field(label, name, type, autocomplete) {
  return el('label', {}, label, el('input', { name, type, autocomplete, required: '' }))
}

function render(root, title, ...children) {
  document.title = title
  root.replaceChildren(el('h1', {}, title), ...children)
}

function status(root) {
  const node = el('p', { role: 'status', 'aria-live': 'polite', class: 'status' })
  root.append(node)
  return text => { node.textContent = text }
}

function form(onSubmit, submitLabel, ...fields) {
  const node = el('form', {}, ...fields, el('button', { type: 'submit' }, submitLabel))
  node.addEventListener('submit', async event => {
    event.preventDefault()
    const button = node.querySelector('button[type=submit]')
    button.disabled = true
    try { await onSubmit(Object.fromEntries(new FormData(node))) } finally { button.disabled = false }
  })
  return node
}

function links(...items) {
  return el('nav', {}, ...items.map(([href, label]) => el('a', { href }, label)))
}

async function main() {
  const root = document.getElementById('app')
  const route = location.pathname.replace(/^\/ui\/?/, '').replace(/\/$/, '') || 'sign-in'
  const config = await request('GET', '/ui/config.json')
  const settings = await request('GET', '/auth/v1/settings').catch(() => ({}))
  const query = new URLSearchParams(location.search)
  const target = allowedRedirect(query.get('redirect_to'), config.site_url, config.redirect_urls)
  const keep = path => `${path}?redirect_to=${encodeURIComponent(target)}`
  const done = session => { location.replace(handoffURL(target, session)) }

  if (route === 'sign-in') {
    render(root, `Sign in to ${config.app}`)
    const say = status(root)
    const signIn = form(async ({ email, password }) => {
      try { done(await request('POST', '/auth/v1/token?grant_type=password', { body: { email, password } })) } catch (err) { say(err.message) }
    }, 'Sign in', field('Email', 'email', 'email', 'username'), field('Password', 'password', 'password', 'current-password'))
    // A magic link needs only the email address from the same form.
    signIn.append(el('button', { type: 'button', class: 'secondary', on: { click: async () => {
      const email = signIn.elements.email
      if (!email.checkValidity()) return email.reportValidity()
      try {
        await request('POST', `/auth/v1/otp?redirect_to=${encodeURIComponent(target)}`, { body: { email: email.value, create_user: false } })
        say('Check your email for a sign-in link.')
      } catch (err) { say(err.message) }
    } } }, 'Email me a sign-in link instead'))
    root.append(signIn)
    const providers = Object.entries(settings.external ?? {}).filter(([name, on]) => on && !['email', 'phone', 'anonymous_users', 'saml'].includes(name))
    if (providers.length) {
      root.append(el('p', { class: 'divider' }, 'or'), el('div', { class: 'providers' }, ...providers.map(([name]) =>
        el('a', { class: 'button secondary', href: `/auth/v1/authorize?provider=${encodeURIComponent(name)}&redirect_to=${encodeURIComponent(target)}` }, `Continue with ${providerName(name)}`))))
    }
    root.append(links(...(settings.disable_signup ? [] : [[keep('/ui/sign-up'), 'Create an account']]), [keep('/ui/forgot-password'), 'Forgot password?']))
  } else if (route === 'sign-up') {
    render(root, `Create your ${config.app} account`)
    const say = status(root)
    root.append(form(async ({ email, password }) => {
      try {
        const result = await request('POST', `/auth/v1/signup?redirect_to=${encodeURIComponent(target)}`, { body: { email, password } })
        if (result?.access_token) return done(result)
        say('Check your email to confirm your address.')
      } catch (err) { say(err.message) }
    }, 'Create account', field('Email', 'email', 'email', 'username'), field('Password', 'password', 'password', 'new-password')))
    root.append(links([keep('/ui/sign-in'), 'Already have an account? Sign in']))
  } else if (route === 'forgot-password') {
    render(root, 'Reset your password')
    const say = status(root)
    const reset = `${location.origin}/ui/reset-password?redirect_to=${encodeURIComponent(target)}`
    root.append(form(async ({ email }) => {
      try {
        await request('POST', `/auth/v1/recover?redirect_to=${encodeURIComponent(reset)}`, { body: { email } })
        say('If an account exists, we sent a reset link.')
      } catch (err) { say(err.message) }
    }, 'Send reset link', field('Email', 'email', 'email', 'username')))
    root.append(links([keep('/ui/sign-in'), 'Back to sign in']))
  } else if (route === 'reset-password') {
    const session = fragmentParams(location.hash)
    history.replaceState(null, '', location.pathname + location.search)
    render(root, 'Choose a new password')
    const say = status(root)
    if (!session.access_token || session.type !== 'recovery') {
      say('This reset link is invalid or expired.')
      root.append(links([keep('/ui/forgot-password'), 'Send a new link']))
      return
    }
    root.append(form(async ({ password }) => {
      try {
        await request('PUT', '/auth/v1/user', { token: session.access_token, body: { password } })
        done({ ...session, expires_in: Number(session.expires_in), expires_at: Number(session.expires_at) || undefined })
      } catch (err) { say(err.message) }
    }, 'Save password', field('New password', 'password', 'password', 'new-password')))
  } else if (route === 'invitation') {
    // The token stays in this tab only, across sign-in or sign-up.
    const fromLink = fragmentParams(location.hash).token
    if (fromLink) sessionStorage.setItem('gregale.invitation', fromLink)
    history.replaceState(null, '', location.pathname)
    const invitation = sessionStorage.getItem('gregale.invitation')
    render(root, `Join an organization on ${config.app}`)
    const say = status(root)
    if (!invitation) { say('This invitation link is incomplete.'); return }
    root.append(el('p', {}, 'Sign in with the invited email address to accept.'))
    root.append(form(async ({ email, password }) => {
      try {
        const session = await request('POST', '/auth/v1/token?grant_type=password', { body: { email, password } })
        const membership = await request('POST', '/authz/v1/invitations/accept', { token: session.access_token, body: { token: invitation } })
        sessionStorage.removeItem('gregale.invitation')
        say(`You joined as ${membership.role}.`)
        root.append(el('a', { class: 'button', href: handoffURL(config.site_url, session) }, `Continue to ${config.app}`))
      } catch (err) { say(err.message) }
    }, 'Sign in and accept', field('Email', 'email', 'email', 'username'), field('Password', 'password', 'password', 'current-password')))
    root.append(links([`/ui/sign-up?redirect_to=${encodeURIComponent(location.origin + '/ui/invitation')}`, 'No account yet? Create one, then reopen this page']))
  } else {
    render(root, 'Page not found')
  }
}

if (typeof document !== 'undefined') {
  main().catch(() => {
    const root = document.getElementById('app')
    root.replaceChildren(el('h1', {}, 'Sign-in is unavailable'), el('p', {}, 'Try again in a moment.'))
  })
}
