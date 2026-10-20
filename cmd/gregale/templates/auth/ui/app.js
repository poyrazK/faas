// Hosted sign-in pages for application Auth (ADR-1190). Plain ES module, no
// dependencies, served under a strict CSP. Sessions are handed to the
// application only in the URL fragment, only to the site URL or an allowed
// redirect URL, and are never written to storage by these pages.

export const routes = ['sign-in', 'sign-up', 'forgot-password', 'reset-password', 'invitation', 'phone', 'sso', 'consent', 'account']

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

// What each OpenID scope lets a client see, in the consent page's words.
const scopeLabels = { openid: 'Know who you are', email: 'See your email address', profile: 'See your name and profile picture', phone: 'See your phone number' }

// consentView is what the consent page says about an authorization: who is
// signed in, what the client may see, which resource it asked for and where
// the person returns. The client's name is its own claim; where it sends
// people back is not.
export function consentView(details) {
  const scopes = String(details.scope ?? '').split(/\s+/).filter(Boolean)
  let destination = details.redirect_uri ?? ''
  try {
    const url = new URL(destination)
    destination = ['http:', 'https:'].includes(url.protocol) ? url.host : url.protocol.replace(/:$/, ' app')
  } catch { destination = 'an unknown address' }
  return {
    name: String(details.client?.name || 'An application').slice(0, 100),
    account: details.user?.email ?? null,
    abilities: scopes.map(scope => scopeLabels[scope] ?? `Use the ${scope} permission`),
    resource: typeof details.resource === 'string' ? details.resource : null,
    destination,
  }
}

// deviceName describes a session's device from its user agent the way other
// account pages do, as in "Chrome on macOS". Order matters: Chrome's agent
// also names Safari, and an iPhone's also names Mac OS X.
export function deviceName(userAgent) {
  const ua = String(userAgent ?? '')
  if (!ua) return 'Unknown device'
  const browser = [['Edg/', 'Edge'], ['OPR/', 'Opera'], ['CriOS/', 'Chrome'], ['FxiOS/', 'Firefox'], ['Firefox/', 'Firefox'], ['Chrome/', 'Chrome'], ['Safari/', 'Safari']].find(([token]) => ua.includes(token))?.[1]
  const system = [['iPhone', 'iPhone'], ['iPad', 'iPad'], ['Android', 'Android'], ['Windows', 'Windows'], ['CrOS', 'ChromeOS'], ['Mac OS X', 'macOS'], ['Linux', 'Linux']].find(([token]) => ua.includes(token))?.[1]
  if (!browser) return system ? `An app on ${system}` : 'An app or script'
  return system ? `${browser} on ${system}` : browser
}

// qrImage turns the QR code GoTrue v2.197.0 returns for a new authenticator
// app, raw SVG markup, into an image URL.
export function qrImage(qr) {
  return String(qr).startsWith('data:') ? qr : `data:image/svg+xml;charset=utf-8,${encodeURIComponent(qr)}`
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

// WebAuthn options arrive as JSON with base64url buffers; browsers want
// ArrayBuffers. These helpers convert both ways without dependencies.
export function fromBase64url(value) {
  const base64 = value.replace(/-/g, '+').replace(/_/g, '/') + '='.repeat((4 - value.length % 4) % 4)
  return Uint8Array.from(atob(base64), c => c.charCodeAt(0)).buffer
}

export function toBase64url(buffer) {
  return btoa(String.fromCharCode(...new Uint8Array(buffer))).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

const credentialIDs = list => list?.map(c => ({ ...c, id: fromBase64url(c.id) }))

export function requestOptions(options) {
  return { ...options, challenge: fromBase64url(options.challenge), ...(options.allowCredentials ? { allowCredentials: credentialIDs(options.allowCredentials) } : {}) }
}

export function creationOptions(options) {
  return {
    ...options, challenge: fromBase64url(options.challenge), user: { ...options.user, id: fromBase64url(options.user.id) },
    ...(options.excludeCredentials ? { excludeCredentials: credentialIDs(options.excludeCredentials) } : {}),
  }
}

// credentialJSON serializes a PublicKeyCredential the way GoTrue verifies it.
export function credentialJSON(credential) {
  if (typeof credential.toJSON === 'function') return credential.toJSON()
  const r = credential.response
  const response = { clientDataJSON: toBase64url(r.clientDataJSON) }
  if (r.attestationObject) Object.assign(response, { attestationObject: toBase64url(r.attestationObject), transports: r.getTransports?.() ?? [] })
  if (r.authenticatorData) Object.assign(response, { authenticatorData: toBase64url(r.authenticatorData), signature: toBase64url(r.signature), ...(r.userHandle ? { userHandle: toBase64url(r.userHandle) } : {}) })
  return { id: credential.id, rawId: toBase64url(credential.rawId), type: credential.type, response, clientExtensionResults: credential.getClientExtensionResults?.() ?? {}, ...(credential.authenticatorAttachment ? { authenticatorAttachment: credential.authenticatorAttachment } : {}) }
}

const providerNames = { github: 'GitHub', gitlab: 'GitLab', linkedin_oidc: 'LinkedIn', slack_oidc: 'Slack', workos: 'WorkOS', x: 'X', azure: 'Microsoft' }

export function providerName(id) {
  return providerNames[id] ?? id.charAt(0).toUpperCase() + id.slice(1)
}

export function errorMessage(body, status) {
  // GoTrue reports a hook's refusal (sign-up restrictions) as msg, with
  // error_code "unknown".
  const hookCode = typeof body?.msg === 'string' && /^[a-z_]+$/.test(body.msg) ? body.msg : undefined
  const code = (body?.error_code === 'unknown' ? undefined : body?.error_code) ?? hookCode ?? body?.code ?? body?.error
  const known = {
    invalid_credentials: 'Wrong email or password.',
    email_not_confirmed: 'Confirm your email address first; check your inbox.',
    user_already_exists: 'An account with this email already exists. Sign in instead.',
    weak_password: 'Choose a longer, less common password.',
    over_email_send_rate_limit: 'Too many emails sent. Wait a minute and try again.',
    email_provider_unconfigured: 'This application cannot send email yet.',
    signup_disabled: 'Sign-up is closed for this application.',
    email_domain_blocked: 'Sign-up is not open to addresses at this domain.',
    disposable_email: 'Use a permanent email address; temporary ones cannot sign up.',
    email_domain_not_allowed: 'Sign-up is open only to addresses at the domains this application allows.',
    signup_restricted: 'Sign up with an email address; this application checks who may join.',
    signup_invitation_required: 'Sign-up is by invitation. Ask for an invitation to this address.',
    waitlisted: 'You are on the waitlist. We will email you when you can sign up.',
    waitlist_full: 'The waitlist is full. Try again later.',
    signup_check_unavailable: 'Sign-up is briefly unavailable. Try again in a minute.',
    invitation_invalid: 'This invitation is no longer valid.',
    invitation_email_mismatch: 'This invitation was sent to a different email address.',
    already_member: 'You are already a member.',
    webauthn_verification_failed: 'That passkey could not be verified. Try again or use your password.',
    webauthn_challenge_not_found: 'The passkey request expired. Try again.',
    passkey_not_found: 'This passkey is no longer registered. Sign in another way.',
    captcha_failed: 'The security check failed or expired. Complete it again.',
    mfa_verification_failed: 'That code is wrong. Try again.',
    mfa_challenge_expired: 'That code has expired. Request a new one.',
    mfa_recovery_codes_locked: 'Too many wrong recovery codes. Wait 15 minutes and try again.',
    oauth_authorization_not_found: 'This request has expired. Go back to the app and try again.',
    otp_expired: 'That code is wrong or has expired. Request a new one.',
    over_sms_send_rate_limit: 'Too many codes sent. Wait a minute and try again.',
    sms_send_failed: 'We could not text this number. Check it and try again.',
    phone_provider_disabled: 'Phone sign-in is turned off for this application.',
    sms_country_not_allowed: 'This application cannot text numbers in that country.',
    validation_failed: 'Check what you entered and try again. Phone numbers need their country code.',
    sso_provider_not_found: 'No organization sign-in is set up for this email domain.',
    sso_provider_disabled: 'Organization sign-in for this domain is turned off.',
    saml_provider_disabled: 'Organization sign-in is turned off for this application.',
    reauthentication_required: 'Sign in again to do this; your sign-in is too old.',
    current_session: 'Use Back to leave; this page signs itself out.',
    impersonation_restricted: 'Support cannot change this from a session opened as you.',
  }
  if (code === 'last_owner') {
    const orgs = Array.isArray(body?.organizations) ? body.organizations.join(', ') : 'an organization'
    return `You are the only owner of ${orgs}, which has other members. Make another member an owner first.`
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

let logoURL = null

function render(root, title, ...children) {
  document.title = title
  root.replaceChildren(...(logoURL ? [el('img', { class: 'logo', src: logoURL, alt: '' })] : []), el('h1', {}, title), ...children)
}

// CAPTCHA: the provider's widget renders into each form that GoTrue checks
// (sign-up, password and magic-link sign-in, password reset), and every
// submission sends a fresh token as gotrue_meta_security.captcha_token.
let captcha = null, captchaScript = null

function loadScript(src) {
  return new Promise((resolve, reject) => {
    const script = el('script', { src, async: '' })
    script.addEventListener('load', resolve)
    script.addEventListener('error', () => reject(new Error('The security check could not load. Reload the page.')))
    document.head.append(script)
  })
}

async function captchaWidget(container) {
  if (!captcha) return null
  await (captchaScript ??= loadScript(captcha.script))
  const api = captcha.provider === 'turnstile' ? globalThis.turnstile : globalThis.hcaptcha
  const id = api.render(container, { sitekey: captcha.site_key })
  return { token: () => api.getResponse(id), reset: () => api.reset(id) }
}

// captchaBody adds the widget's token to a request body; the widget is reset
// afterwards because a token is accepted once.
export function captchaBody(body, token) {
  if (token === undefined) return body
  if (!token) throw new Error('Complete the security check first.')
  return { ...body, gotrue_meta_security: { captcha_token: token } }
}

function status(root) {
  const node = el('p', { role: 'status', 'aria-live': 'polite', class: 'status' })
  root.append(node)
  return text => { node.textContent = text }
}

// form renders the CAPTCHA widget when CAPTCHA is on; plainForm never does,
// for requests GoTrue does not check (verifying a code, a new password).
function form(onSubmit, submitLabel, ...fields) {
  return buildForm(Boolean(captcha), onSubmit, submitLabel, fields)
}

function plainForm(onSubmit, submitLabel, ...fields) {
  return buildForm(false, onSubmit, submitLabel, fields)
}

function buildForm(withCaptcha, onSubmit, submitLabel, fields) {
  const check = withCaptcha ? el('div', { class: 'captcha' }) : null
  const node = el('form', {}, ...fields, ...(check ? [check] : []), el('button', { type: 'submit' }, submitLabel))
  const widget = check ? captchaWidget(check) : Promise.resolve(null)
  // protect(body) adds the token for one request and then resets the widget.
  node.protect = async (body, send) => {
    const w = await widget
    try { return await send(captchaBody(body, w ? w.token() : undefined)) } finally { w?.reset() }
  }
  node.addEventListener('submit', async event => {
    event.preventDefault()
    const button = node.querySelector('button[type=submit]')
    button.disabled = true
    try { await onSubmit(Object.fromEntries(new FormData(node)), node.protect) } finally { button.disabled = false }
  })
  return node
}

const passkeysAvailable = settings => settings.passkeys_enabled === true && typeof PublicKeyCredential !== 'undefined'

// A cancelled or unavailable passkey prompt is not an error worth a message.
function passkeyFailure(err) {
  return err?.name === 'NotAllowedError' || err?.name === 'AbortError' ? 'Passkey sign-in was cancelled.' : err.message
}

// GoTrue checks CAPTCHA when a passkey sign-in starts, so the options
// request carries the sign-in form's token.
async function signInWithPasskey(protect) {
  const { challenge_id, options } = await protect({}, body => request('POST', '/auth/v1/passkeys/authentication/options', { body }))
  const credential = await navigator.credentials.get({ publicKey: requestOptions(options) })
  return request('POST', '/auth/v1/passkeys/authentication/verify', { body: { challenge_id, credential: credentialJSON(credential) } })
}

async function addPasskey(session) {
  const { challenge_id, options } = await request('POST', '/auth/v1/passkeys/registration/options', { token: session.access_token, body: {} })
  const credential = await navigator.credentials.create({ publicKey: creationOptions(options) })
  await request('POST', '/auth/v1/passkeys/registration/verify', { token: session.access_token, body: { challenge_id, credential: credentialJSON(credential) } })
}

// After a password sign-in, offer a passkey to users who have none; either
// way the session then goes to the application.
async function offerPasskey(root, settings, session, done) {
  if (!passkeysAvailable(settings)) return done(session)
  const existing = await request('GET', '/auth/v1/passkeys', { token: session.access_token }).catch(() => null)
  if (!Array.isArray(existing) || existing.length > 0) return done(session)
  render(root, 'Sign in faster next time')
  root.append(el('p', {}, 'Add a passkey to sign in with your fingerprint, face or device PIN instead of a password.'))
  const say = status(root)
  root.append(el('div', { class: 'providers' },
    el('button', { type: 'button', on: { click: async event => {
      event.target.disabled = true
      try { await addPasskey(session); done(session) } catch (err) { say(passkeyFailure(err)); event.target.disabled = false }
    } } }, 'Add a passkey'),
    el('button', { type: 'button', class: 'secondary', on: { click: () => done(session) } }, 'Not now')))
}

// web3Message writes the message a wallet signs, in the grammar GoTrue's
// parsers accept (EIP-4361 for Ethereum, its Solana equivalent); it matches
// @gregale/auth's.
export function web3Message(chain, { domain, address, uri, statement, chainId, nonce, issuedAt }) {
  return [
    `${domain} wants you to sign in with your ${chain === 'ethereum' ? 'Ethereum' : 'Solana'} account:`, address, '',
    ...(statement ? [statement.replace(/[\r\n]+/g, ' '), ''] : []),
    `URI: ${uri}`, 'Version: 1', ...(chainId !== undefined ? [`Chain ID: ${chainId}`] : []), `Nonce: ${nonce}`, `Issued At: ${issuedAt}`,
  ].join('\n')
}

function injectedWallet(chain) {
  return chain === 'ethereum' ? globalThis.ethereum : (globalThis.phantom?.solana ?? globalThis.solana)
}

// signInWithWallet asks the browser's wallet to sign a message naming this
// page and returns GoTrue's session.
async function signInWithWallet(chain) {
  const wallet = injectedWallet(chain)
  const uri = `${location.origin}/ui/sign-in`
  const fields = { domain: location.host, uri, statement: `Sign in to ${document.title.replace(/^Sign in to /, '')}`, nonce: Array.from(crypto.getRandomValues(new Uint8Array(16)), b => b.toString(16).padStart(2, '0')).join(''), issuedAt: new Date().toISOString() }
  let message, signature
  if (chain === 'ethereum') {
    const [address] = await wallet.request({ method: 'eth_requestAccounts' })
    const chainId = Number.parseInt(await wallet.request({ method: 'eth_chainId' }), 16)
    message = web3Message(chain, { ...fields, address, chainId })
    signature = await wallet.request({ method: 'personal_sign', params: [`0x${Array.from(new TextEncoder().encode(message), b => b.toString(16).padStart(2, '0')).join('')}`, address] })
  } else {
    if (!wallet.publicKey) await wallet.connect()
    message = web3Message(chain, { ...fields, address: wallet.publicKey.toBase58() })
    const signed = await wallet.signMessage(new TextEncoder().encode(message), 'utf8')
    signature = btoa(String.fromCharCode(...(signed instanceof Uint8Array ? signed : signed.signature)))
  }
  return request('POST', '/auth/v1/token?grant_type=web3', { body: { chain, message, signature } })
}

// walletFailure explains a wallet's refusal (EIP-1193 code 4001) plainly.
function walletFailure(err) {
  return err?.code === 4001 || /reject/i.test(err?.message ?? '') ? 'The wallet did not sign. Try again when you are ready.' : (err?.message ?? 'The wallet could not sign in.')
}

// tokenAAL reads the assurance level of an access token GoTrue just issued.
export function tokenAAL(token) {
  try { return JSON.parse(new TextDecoder().decode(fromBase64url(token.split('.')[1]))).aal ?? 'aal1' } catch { return 'aal1' }
}

// secondFactors returns a user's verified factors that can be challenged on
// these pages, and whether recovery codes can stand in for them.
export function secondFactors(user) {
  const verified = (user?.factors ?? []).filter(factor => factor.status === 'verified')
  return { factors: verified.filter(factor => factor.factor_type === 'totp' || factor.factor_type === 'phone'), recovery: verified.some(factor => factor.factor_type === 'recovery_code') }
}

// joinedMessage confirms an accepted invitation. An organization that
// requires a second factor (sql/0016) is usable only from an aal2 session.
export function joinedMessage(membership, aal) {
  const joined = `You joined as ${membership.role}.`
  if (!membership.mfa_required || aal === 'aal2') return joined
  return `${joined} This organization requires two-step verification: add a second step to your account, then sign in again, before you use it.`
}

// completeSignIn asks a user with a verified second factor to complete it
// after any first-factor sign-in on these pages, so the session handed to
// the application is aal2; then finish(session) runs.
async function completeSignIn(root, session, finish) {
  if (tokenAAL(session.access_token) === 'aal2') return finish(session)
  const user = await request('GET', '/auth/v1/user', { token: session.access_token }).catch(() => null)
  const { factors, recovery } = secondFactors(user)
  if (!factors.length) return finish(session)
  const token = session.access_token
  render(root, 'Two-step verification')
  const say = status(root)
  const area = el('div')
  root.append(area)
  const useFactor = async factor => {
    let challenge
    try { challenge = await request('POST', `/auth/v1/factors/${encodeURIComponent(factor.id)}/challenge`, { token, body: {} }) } catch (err) { return say(err.message) }
    say(factor.factor_type === 'phone' ? `We texted a code to ${factor.phone ?? 'your phone'}.` : `Enter the code from ${factor.friendly_name || 'your authenticator app'}.`)
    area.replaceChildren(plainForm(async ({ code }) => {
      try { finish(await request('POST', `/auth/v1/factors/${encodeURIComponent(factor.id)}/verify`, { token, body: { challenge_id: challenge.id, code: code.trim() } })) } catch (err) { say(err.message) }
    }, 'Verify', el('label', {}, 'Code', el('input', { name: 'code', inputmode: 'numeric', autocomplete: 'one-time-code', required: '' }))))
  }
  const useRecovery = () => {
    say('Enter one of your recovery codes. Each code works once.')
    area.replaceChildren(plainForm(async ({ code }) => {
      try { finish(await request('POST', '/auth/v1/factors/recovery-codes/verify', { token, body: { code: code.trim() } })) } catch (err) { say(err.message) }
    }, 'Verify', el('label', {}, 'Recovery code', el('input', { name: 'code', autocomplete: 'off', spellcheck: 'false', required: '' }))))
  }
  const choices = [...factors.slice(1).map(factor => [factor.factor_type === 'phone' ? 'Text me a code instead' : `Use ${factor.friendly_name || 'an authenticator app'} instead`, () => useFactor(factor)]), ...(recovery ? [['Use a recovery code', useRecovery]] : [])]
  if (choices.length) root.append(el('div', { class: 'providers' }, ...choices.map(([label, onClick]) => el('button', { type: 'button', class: 'secondary', on: { click: onClick } }, label))))
  await useFactor(factors[0])
}

function links(...items) {
  return el('nav', {}, ...items.map(([href, label]) => el('a', { href }, label)))
}

async function main() {
  const root = document.getElementById('app')
  const route = location.pathname.replace(/^\/ui\/?/, '').replace(/\/$/, '') || 'sign-in'
  const config = await request('GET', '/ui/config.json')
  logoURL = config.logo_url ?? null
  captcha = config.captcha ?? null
  const settings = await request('GET', '/auth/v1/settings').catch(() => ({}))
  const query = new URLSearchParams(location.search)
  // Signing in for the consent or account page returns to it with the session.
  const target = allowedRedirect(query.get('redirect_to'), config.site_url, [...config.redirect_urls, `${location.origin}/ui/consent`, `${location.origin}/ui/account`])
  const keep = path => `${path}?redirect_to=${encodeURIComponent(target)}`
  const done = session => { location.replace(handoffURL(target, session)) }

  if (route === 'sign-in') {
    render(root, `Sign in to ${config.app}`)
    const say = status(root)
    const signIn = form(async ({ email, password }, protect) => {
      let session
      try { session = await protect({ email, password }, body => request('POST', '/auth/v1/token?grant_type=password', { body })) } catch (err) { return say(err.message) }
      await completeSignIn(root, session, verified => offerPasskey(root, settings, verified, done))
    }, 'Sign in', field('Email', 'email', 'email', 'username webauthn'), field('Password', 'password', 'password', 'current-password'))
    // A magic link needs only the email address from the same form.
    signIn.append(el('button', { type: 'button', class: 'secondary', on: { click: async () => {
      const email = signIn.elements.email
      if (!email.checkValidity()) return email.reportValidity()
      try {
        await signIn.protect({ email: email.value, create_user: false }, body => request('POST', `/auth/v1/otp?redirect_to=${encodeURIComponent(target)}`, { body }))
        say('Check your email for a sign-in link.')
      } catch (err) { say(err.message) }
    } } }, 'Email me a sign-in link instead'))
    root.append(signIn)
    const providers = Object.entries(settings.external ?? {}).filter(([name, on]) => on && !['email', 'phone', 'anonymous_users', 'saml', 'web3_ethereum', 'web3_solana'].includes(name))
    const passkey = passkeysAvailable(settings) ? [el('button', { type: 'button', class: 'secondary', on: { click: async event => {
      event.target.disabled = true
      try { await completeSignIn(root, await signInWithPasskey(signIn.protect), done) } catch (err) { say(passkeyFailure(err)); event.target.disabled = false }
    } } }, 'Sign in with a passkey')] : []
    // A wallet the browser offers (window.ethereum, window.solana) signs in
    // directly; GoTrue verifies the signature and creates the user once.
    const wallets = ['ethereum', 'solana'].filter(chain => config.web3?.[chain] && injectedWallet(chain)).map(chain => el('button', { type: 'button', class: 'secondary', on: { click: async event => {
      event.target.disabled = true
      try { await completeSignIn(root, await signInWithWallet(chain), done) } catch (err) { say(walletFailure(err)); event.target.disabled = false }
    } } }, `Sign in with ${chain === 'ethereum' ? 'an Ethereum' : 'a Solana'} wallet`))
    passkey.push(...wallets)
    if (providers.length || passkey.length) {
      root.append(el('p', { class: 'divider' }, 'or'), el('div', { class: 'providers' }, ...passkey, ...providers.map(([name]) =>
        el('a', { class: 'button secondary', href: `/auth/v1/authorize?provider=${encodeURIComponent(name)}&redirect_to=${encodeURIComponent(target)}` }, `Continue with ${providerName(name)}`))))
    }
    const others = [...(settings.external?.phone ? [[keep('/ui/phone'), 'Sign in with a phone number']] : []), ...(settings.saml_enabled ? [[keep('/ui/sso'), 'Sign in with your organization (SSO)']] : [])]
    if (others.length) root.append(el('div', { class: 'providers' }, ...others.map(([href, label]) => el('a', { class: 'button secondary', href }, label))))
    root.append(links(...(settings.disable_signup ? [] : [[keep('/ui/sign-up'), 'Create an account']]), [keep('/ui/forgot-password'), 'Forgot password?']))
  } else if (route === 'phone') {
    // GoTrue texts a one-time code; a new number signs up unless sign-up is
    // closed. Only sending the code is CAPTCHA-checked.
    render(root, `Sign in to ${config.app} with your phone`)
    const say = status(root)
    const sendCode = form(async ({ phone }, protect) => {
      try {
        await protect({ phone, channel: 'sms', create_user: !settings.disable_signup }, body => request('POST', '/auth/v1/otp', { body }))
      } catch (err) { return say(err.message) }
      say(`We texted a code to ${phone}.`)
      sendCode.replaceWith(plainForm(async ({ code }) => {
        let session
        try { session = await request('POST', '/auth/v1/verify', { body: { type: 'sms', phone, token: code.trim() } }) } catch (err) { return say(err.message) }
        await completeSignIn(root, session, verified => offerPasskey(root, settings, verified, done))
      }, 'Sign in', el('label', {}, 'Code', el('input', { name: 'code', inputmode: 'numeric', autocomplete: 'one-time-code', pattern: '[0-9 ]{6,12}', required: '' }))))
    }, 'Text me a code', el('label', {}, 'Phone number, with country code', el('input', { name: 'phone', type: 'tel', autocomplete: 'tel', placeholder: '+44 7700 900123', required: '' })))
    root.append(sendCode, links([keep('/ui/phone'), 'Use a different number or get a new code'], [keep('/ui/sign-in'), 'Sign in another way']))
  } else if (route === 'sso') {
    // GoTrue finds the identity provider by the email's domain and returns
    // its sign-in URL; after it, GoTrue returns to the target with a session.
    render(root, `Sign in to ${config.app} with your organization`)
    const say = status(root)
    root.append(form(async ({ email }, protect) => {
      const domain = email.trim().toLowerCase().split('@').pop()
      try {
        const { url } = await protect({ domain, redirect_to: target, skip_http_redirect: true }, body => request('POST', '/auth/v1/sso', { body }))
        if (new URL(url).protocol !== 'https:') throw new Error('Organization sign-in is misconfigured.')
        location.assign(url)
      } catch (err) { say(err.message) }
    }, 'Continue', el('label', {}, 'Work email', el('input', { name: 'email', type: 'email', autocomplete: 'username', required: '' }))))
    root.append(links([keep('/ui/sign-in'), 'Sign in another way']))
  } else if (route === 'sign-up') {
    render(root, `Create your ${config.app} account`)
    const say = status(root)
    root.append(form(async ({ email, password }, protect) => {
      try {
        const result = await protect({ email, password }, body => request('POST', `/auth/v1/signup?redirect_to=${encodeURIComponent(target)}`, { body }))
        if (result?.access_token) return offerPasskey(root, settings, result, done)
        say('Check your email to confirm your address.')
      } catch (err) { say(err.message) }
    }, 'Create account', field('Email', 'email', 'email', 'username'), field('Password', 'password', 'password', 'new-password')))
    root.append(links([keep('/ui/sign-in'), 'Already have an account? Sign in']))
  } else if (route === 'forgot-password') {
    render(root, 'Reset your password')
    const say = status(root)
    const reset = `${location.origin}/ui/reset-password?redirect_to=${encodeURIComponent(target)}`
    root.append(form(async ({ email }, protect) => {
      try {
        await protect({ email }, body => request('POST', `/auth/v1/recover?redirect_to=${encodeURIComponent(reset)}`, { body }))
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
    root.append(plainForm(async ({ password }) => {
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
    root.append(form(async ({ email, password }, protect) => {
      try {
        const session = await protect({ email, password }, body => request('POST', '/auth/v1/token?grant_type=password', { body }))
        const membership = await request('POST', '/authz/v1/invitations/accept', { token: session.access_token, body: { token: invitation } })
        sessionStorage.removeItem('gregale.invitation')
        // Like every sign-in here, a user with a second factor confirms it,
        // so the session handed over can use an organization requiring one.
        await completeSignIn(root, session, verified => {
          render(root, `Join an organization on ${config.app}`)
          const joined = status(root)
          joined(joinedMessage(membership, tokenAAL(verified.access_token)))
          if (membership.mfa_required && tokenAAL(verified.access_token) !== 'aal2') root.append(links([`/ui/account?redirect_to=${encodeURIComponent(config.site_url)}`, 'Add a second step on your account page']))
          root.append(el('a', { class: 'button', href: handoffURL(config.site_url, verified) }, `Continue to ${config.app}`))
        })
      } catch (err) { say(err.message) }
    }, 'Sign in and accept', field('Email', 'email', 'email', 'username'), field('Password', 'password', 'password', 'current-password')))
    root.append(links([`/ui/sign-up?redirect_to=${encodeURIComponent(location.origin + '/ui/invitation')}`, 'No account yet? Create one, then reopen this page']))
  } else if (route === 'consent') {
    await consent(root, config, query)
  } else if (route === 'account') {
    await account(root, config, query)
  } else {
    render(root, 'Page not found')
  }
}

// consent approves or denies an OAuth client's request. GoTrue's server
// sends people here; they sign in on these pages first, and the session
// made for the decision ends after it.
async function consent(root, config, query) {
  const id = query.get('authorization_id') ?? ''
  const here = `${location.origin}/ui/consent?authorization_id=${encodeURIComponent(id)}`
  const session = fragmentParams(location.hash)
  history.replaceState(null, '', location.pathname + location.search)
  render(root, 'Allow access?')
  const say = status(root)
  if (!/^[A-Za-z0-9_-]{1,128}$/.test(id)) return say('This request is incomplete. Go back to the app and try again.')
  if (!session.access_token) return location.replace(`/ui/sign-in?redirect_to=${encodeURIComponent(here)}`)
  const end = () => request('POST', '/auth/v1/logout?scope=local', { token: session.access_token }).catch(() => {})
  let details
  try { details = await request('GET', `/auth/v1/oauth/authorizations/${encodeURIComponent(id)}`, { token: session.access_token }) } catch (err) { await end(); return say(err.message) }
  // A client already approved for these permissions goes straight back.
  if (details.redirect_url) { await end(); return location.replace(details.redirect_url) }
  const view = consentView(details)
  render(root, `${view.name} wants to use your ${config.app} account`)
  const decide = async action => {
    for (const button of root.querySelectorAll('button')) button.disabled = true
    try {
      const { redirect_url: next } = await request('POST', `/auth/v1/oauth/authorizations/${encodeURIComponent(id)}/consent`, { token: session.access_token, body: { action } })
      await end()
      location.replace(next)
    } catch (err) {
      say(err.message)
      for (const button of root.querySelectorAll('button')) button.disabled = false
    }
  }
  root.append(
    ...(view.account ? [el('p', {}, `Signed in as ${view.account}.`)] : []),
    el('p', {}, `${view.name} will be able to:`),
    el('ul', {}, ...view.abilities.map(line => el('li', {}, line))),
    ...(view.resource ? [el('p', {}, `It asks for access to ${view.resource}.`)] : []),
    el('p', {}, `Afterwards you return to ${view.destination}. Only allow apps you trust.`),
    el('div', { class: 'providers' },
      el('button', { type: 'button', on: { click: () => decide('approve') } }, 'Allow'),
      el('button', { type: 'button', class: 'secondary', on: { click: () => decide('deny') } }, 'Deny')),
    el('nav', {}, el('a', { href: '#', on: { click: async event => {
      // Another account starts after this session ends.
      event.preventDefault()
      await end()
      location.replace(`/ui/sign-in?redirect_to=${encodeURIComponent(here)}`)
    } } }, 'Use another account')))
}

const when = value => { const date = new Date(value); return Number.isNaN(date.getTime()) ? 'unknown' : date.toLocaleString() }

function section(title, ...children) {
  return el('section', {}, el('h2', {}, title), ...children.filter(Boolean))
}

function row(label, ...controls) {
  return el('div', { class: 'row' }, el('span', {}, label), ...controls)
}

function actions(list) {
  return list.length ? el('div', { class: 'providers' }, ...list.map(([label, onClick]) => el('button', { type: 'button', class: 'secondary', on: { click: onClick } }, label))) : null
}

// confirmButton acts on a second click within five seconds, for changes
// that weaken or end something.
function confirmButton(label, action) {
  const button = el('button', { type: 'button', class: 'secondary' }, label)
  let armed = null
  button.addEventListener('click', async () => {
    if (!armed) {
      button.textContent = `${label}? Click again`
      armed = setTimeout(() => { armed = null; button.textContent = label }, 5000)
      return
    }
    clearTimeout(armed)
    armed = null
    button.disabled = true
    try { await action() } finally { button.disabled = false; button.textContent = label }
  })
  return button
}

// account lets a signed-in person manage their own account. Like the consent
// page it signs in first, completing any second factor, and keeps that
// session only in this tab's memory; leaving the page ends it.
async function account(root, config, query) {
  const back = allowedRedirect(query.get('redirect_to'), config.site_url, config.redirect_urls)
  const here = `${location.origin}/ui/account?redirect_to=${encodeURIComponent(back)}`
  let session = fragmentParams(location.hash)
  history.replaceState(null, '', location.pathname + location.search)
  if (!session.access_token) return location.replace(`/ui/sign-in?redirect_to=${encodeURIComponent(here)}`)
  root.classList.add('wide')
  render(root, `Your ${config.app} account`)
  const say = status(root)
  const area = el('div')
  let ended = false
  const end = async () => {
    if (ended) return
    ended = true
    await request('POST', '/auth/v1/logout?scope=local', { token: session.access_token }).catch(() => {})
  }
  // A closed tab ends the session too; keepalive lets the request finish.
  addEventListener('pagehide', () => {
    if (ended) return
    ended = true
    fetch('/auth/v1/logout?scope=local', { method: 'POST', keepalive: true, headers: { Authorization: `Bearer ${session.access_token}` } }).catch(() => {})
  })
  root.append(area, el('div', { class: 'providers' }, el('button', { type: 'button', on: { click: async () => { await end(); location.replace(back) } } }, `Back to ${config.app}`)))
  let expiresAt = Number(session.expires_at) || Math.floor(Date.now() / 1000) + Number(session.expires_in || 0)
  const ctx = {
    say, here, config,
    options: await request('GET', '/settings/v1').catch(() => ({})),
    adopt(next) { session = next; expiresAt = next.expires_at ?? Math.floor(Date.now() / 1000) + next.expires_in },
    // token renews the session shortly before it expires.
    async token() {
      if (expiresAt - 30 <= Date.now() / 1000) ctx.adopt(await request('POST', '/auth/v1/token?grant_type=refresh_token', { body: { refresh_token: session.refresh_token } }))
      return session.access_token
    },
    async call(method, path, body) { return request(method, path, { token: await ctx.token(), body }) },
    async act(work) { try { await work() } catch (err) { say(passkeyFailure(err)) } },
    // Linking a provider leaves through its sign-in and returns with a new
    // session; this one stays listed under sessions until it is signed out.
    // Deleting the account ends every session with it.
    leave() { ended = true },
    back,
    async refresh() {
      let user
      try { user = await ctx.call('GET', '/auth/v1/user') } catch (err) { return say(err.message) }
      const parts = await Promise.all([profileSection(ctx, user), mfaSection(ctx, user), passkeysSection(ctx), identitiesSection(ctx, user), connectedAppsSection(ctx), joinableSection(ctx), sessionsSection(ctx), deletionSection(ctx, root)]
        .map(part => Promise.resolve(part).catch(err => { say(err.message); return null })))
      area.replaceChildren(...parts.filter(Boolean))
    },
  }
  await ctx.refresh()
}

function profileSection(ctx, user) {
  const lines = [
    el('p', {}, `Email: ${user.email || 'none'}${user.email && !user.email_confirmed_at ? ' (not confirmed)' : ''}`),
    ...(user.phone ? [el('p', {}, `Phone: ${user.phone}`)] : []),
  ]
  // Organization sign-in owns the email address and the password.
  if (user.is_sso_user) return section('Profile', ...lines, el('p', {}, 'Your organization manages your email address and password.'))
  const email = plainForm(async values => {
    try {
      await ctx.call('PUT', '/auth/v1/user', { email: values.email })
      email.reset()
      ctx.say(`We sent a confirmation link. The change completes once ${values.email} is confirmed.`)
    } catch (err) { ctx.say(err.message) }
  }, 'Change email', field('New email', 'email', 'email', 'email'))
  const hasPassword = (user.identities ?? []).some(identity => identity.provider === 'email')
  const password = plainForm(async values => {
    try {
      await ctx.call('PUT', '/auth/v1/user', { password: values.password })
      password.reset()
      ctx.say('Your password is saved.')
    } catch (err) { ctx.say(err.message) }
  }, hasPassword ? 'Change password' : 'Set a password',
  // Password managers file the new password under this account.
  el('input', { type: 'hidden', name: 'username', autocomplete: 'username', value: user.email ?? '' }), field('New password', 'password', 'password', 'new-password'))
  return section('Profile', ...lines, email, password)
}

// codeForm verifies a factor with a code: an existing challenge (a text
// message already sent) or, for an authenticator app, one made on submit.
// Verifying returns an aal2 session, which replaces this page's; GoTrue
// also ends every session of the user that has not passed a second step.
function codeForm(ctx, factorId, challengeId, label) {
  return plainForm(async ({ code }) => {
    try {
      const factor = encodeURIComponent(factorId)
      const challenge = challengeId ?? (await ctx.call('POST', `/auth/v1/factors/${factor}/challenge`, {})).id
      ctx.adopt(await ctx.call('POST', `/auth/v1/factors/${factor}/verify`, { challenge_id: challenge, code: code.trim() }))
      await ctx.refresh()
      ctx.say('Two-step verification is on. Devices signed in without it were signed out.')
    } catch (err) { ctx.say(err.message) }
  }, label, el('label', {}, 'Code', el('input', { name: 'code', inputmode: 'numeric', autocomplete: 'one-time-code', required: '' })))
}

async function mfaSection(ctx, user) {
  const mfa = ctx.options.mfa ?? {}
  const verified = (user.factors ?? []).filter(factor => factor.status === 'verified')
  const factors = verified.filter(factor => factor.factor_type === 'totp' || factor.factor_type === 'phone')
  const recovery = verified.some(factor => factor.factor_type === 'recovery_code')
  if (!mfa.totp && !mfa.phone && !factors.length) return null
  const work = el('div')
  const list = factors.map(factor => row(factor.factor_type === 'phone' ? `Text message to ${factor.phone ?? 'your phone'}` : factor.friendly_name || 'Authenticator app',
    confirmButton('Remove', () => ctx.act(async () => { await ctx.call('DELETE', `/auth/v1/factors/${encodeURIComponent(factor.id)}`); await ctx.refresh() }))))
  if (recovery) {
    const state = await ctx.call('GET', '/auth/v1/factors/recovery-codes').catch(() => null)
    if (state) list.push(row(`Recovery codes: ${state.remaining ?? 0} of ${state.total} left`))
  }
  const choices = []
  if (mfa.totp) {
    choices.push(['Add an authenticator app', () => ctx.act(async () => {
      const factor = await ctx.call('POST', '/auth/v1/factors', { factor_type: 'totp' })
      work.replaceChildren(el('p', {}, 'Scan this code with your authenticator app, or enter the key by hand. Then enter the code it shows.'),
        el('img', { class: 'qr', src: qrImage(factor.totp.qr_code), alt: 'QR code for your authenticator app' }), el('p', {}, el('code', {}, factor.totp.secret)),
        codeForm(ctx, factor.id, null, 'Turn on'))
    })])
  }
  if (mfa.phone) {
    choices.push(['Add text message codes', () => {
      work.replaceChildren(plainForm(async ({ phone }) => ctx.act(async () => {
        const factor = await ctx.call('POST', '/auth/v1/factors', { factor_type: 'phone', phone })
        const challenge = await ctx.call('POST', `/auth/v1/factors/${encodeURIComponent(factor.id)}/challenge`, {})
        ctx.say(`We texted a code to ${phone}.`)
        work.replaceChildren(codeForm(ctx, factor.id, challenge.id, 'Turn on'))
      }), 'Text me a code', el('label', {}, 'Phone number, with country code', el('input', { name: 'phone', type: 'tel', autocomplete: 'tel', required: '' }))))
    }])
  }
  if (mfa.recovery_codes && factors.length) {
    choices.push([recovery ? 'Replace recovery codes' : 'Create recovery codes', () => ctx.act(async () => {
      const codes = await ctx.call('POST', recovery ? '/auth/v1/factors/recovery-codes/regenerate' : '/auth/v1/factors/recovery-codes', {})
      work.replaceChildren(el('p', {}, 'Save these codes somewhere safe; they are shown only now. Each one works once in place of your second step.'),
        el('ul', { class: 'codes' }, ...(codes.codes ?? []).map(code => el('li', {}, el('code', {}, code)))))
    })])
  }
  return section('Two-step verification', el('p', {}, factors.length ? 'Sign-ins are confirmed with:' : 'Add a second step to protect your account.'), ...list, actions(choices), work)
}

async function passkeysSection(ctx) {
  if (!ctx.options.passkeys?.enabled) return null
  const passkeys = (await ctx.call('GET', '/auth/v1/passkeys')) ?? []
  const list = passkeys.map(passkey => row(`${passkey.friendly_name || 'Passkey'}, added ${when(passkey.created_at)}${passkey.last_used_at ? `, last used ${when(passkey.last_used_at)}` : ''}`,
    confirmButton('Remove', () => ctx.act(async () => { await ctx.call('DELETE', `/auth/v1/passkeys/${encodeURIComponent(passkey.id)}`); await ctx.refresh() }))))
  const add = typeof PublicKeyCredential === 'undefined' ? [] : [['Add a passkey', () => ctx.act(async () => { await addPasskey({ access_token: await ctx.token() }); await ctx.refresh() })]]
  return section('Passkeys', ...(list.length ? list : [el('p', {}, 'No passkeys yet.')]), actions(add))
}

function identitiesSection(ctx, user) {
  const identities = user.identities ?? []
  const linking = ctx.options.identity_linking === true
  const list = identities.map(identity => row(`${providerName(identity.provider)}${identity.identity_data?.email ? ` (${identity.identity_data.email})` : ''}`,
    ...(linking && identities.length > 1 ? [confirmButton('Unlink', () => ctx.act(async () => { await ctx.call('DELETE', `/auth/v1/user/identities/${encodeURIComponent(identity.identity_id)}`); await ctx.refresh() }))] : [])))
  const linkable = linking ? (ctx.options.providers ?? []).filter(provider => !identities.some(identity => identity.provider === provider)) : []
  const links = linkable.map(provider => [`Link ${providerName(provider)}`, () => ctx.act(async () => {
    const { url } = await ctx.call('GET', `/auth/v1/user/identities/authorize?${new URLSearchParams({ provider, redirect_to: ctx.here, skip_http_redirect: 'true' })}`)
    if (new URL(url).protocol !== 'https:') throw new Error('This sign-in method is misconfigured.')
    ctx.leave()
    location.assign(url)
  })])
  return section('Sign-in methods', ...list, actions(links))
}

async function connectedAppsSection(ctx) {
  if (!ctx.options.oauth?.enabled) return null
  const grants = (await ctx.call('GET', '/auth/v1/user/oauth/grants')) ?? []
  const list = grants.map(grant => row(`${grant.client?.name || 'An application'}, allowed ${when(grant.granted_at)}`,
    confirmButton('Revoke', () => ctx.act(async () => { await ctx.call('DELETE', `/auth/v1/user/oauth/grants?client_id=${encodeURIComponent(grant.client.id)}`); await ctx.refresh() }))))
  return section('Connected apps', ...(list.length ? list : [el('p', {}, 'No other apps can use your account.')]))
}

// joinableSection offers the organization that verified the person's email
// domain and lets people at it join (sql/0017).
async function joinableSection(ctx) {
  if (ctx.options.organization_domains?.joining !== true) return null
  const { organizations } = await ctx.call('GET', '/authz/v1/suggested-organizations')
  if (!organizations.length) return null
  return section('Organizations you can join', el('p', {}, 'Your organization verified your email domain, so you can join it.'),
    ...organizations.map(org => row(`${org.name} (${org.domain}), as ${org.role}`,
      el('button', { type: 'button', class: 'secondary', on: { click: () => ctx.act(async () => {
        await ctx.call('POST', `/authz/v1/orgs/${encodeURIComponent(org.id)}/join`, {})
        await ctx.refresh()
        ctx.say(`You joined ${org.name}.`)
      }) } }, 'Join'))))
}

async function sessionsSection(ctx) {
  const { sessions } = await ctx.call('GET', '/authz/v1/sessions')
  const list = sessions.map(s => row(`${deviceName(s.user_agent)}${s.ip ? `, ${s.ip}` : ''}: ${s.current ? 'this page' : `active ${when(s.last_active_at)}`}${s.impersonated ? ` (opened by ${ctx.config.app} support)` : ''}`,
    ...(s.current ? [] : [confirmButton('Sign out', () => ctx.act(async () => { await ctx.call('DELETE', `/authz/v1/sessions/${encodeURIComponent(s.id)}`); await ctx.refresh() }))])))
  const others = sessions.some(s => !s.current) ? [['Sign out everywhere else', () => ctx.act(async () => {
    await ctx.call('POST', '/auth/v1/logout?scope=others')
    await ctx.refresh()
    ctx.say(`Signed out everywhere else, including ${ctx.config.app} in this browser.`)
  })]] : []
  return section('Where you are signed in', el('p', {}, `This page has its own session; ${ctx.config.app} in this browser is listed separately.`), ...list, actions(others))
}

// deletionSection lets people delete their own account when the app allows
// it. The page's session is a fresh sign-in, which the Auth app requires.
function deletionSection(ctx, root) {
  if (ctx.options.self_deletion !== true) return null
  const confirmation = 'delete my account'
  const node = section('Delete your account',
    el('p', {}, `This deletes your ${ctx.config.app} account and signs you out everywhere. It cannot be undone. Type "${confirmation}" to confirm.`),
    plainForm(async ({ confirm }) => {
      if (confirm.trim() !== confirmation) return ctx.say(`Type "${confirmation}" to confirm.`)
      try {
        await ctx.call('DELETE', '/authz/v1/account')
        ctx.leave()
        render(root, 'Your account is deleted')
        root.append(el('p', {}, `Your ${ctx.config.app} account and its sign-in methods are gone.`), el('a', { class: 'button', href: ctx.back }, `Back to ${ctx.config.app}`))
      } catch (err) { ctx.say(err.message) }
    }, 'Delete my account', el('label', {}, 'Confirmation', el('input', { name: 'confirm', autocomplete: 'off', spellcheck: 'false', required: '' }))))
  node.classList.add('danger')
  return node
}

if (typeof document !== 'undefined') {
  main().catch(() => {
    const root = document.getElementById('app')
    root.replaceChildren(el('h1', {}, 'Sign-in is unavailable'), el('p', {}, 'Try again in a moment.'))
  })
}
