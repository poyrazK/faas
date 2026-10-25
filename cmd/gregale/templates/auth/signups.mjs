import { readFileSync } from 'node:fs'
import { limits } from './config.mjs'
export { signupAccessModes, signupPolicy } from './config.mjs'
import { HTTPError } from './authz.mjs'
import { loadEmailTemplates, renderEmail, templateFor } from './email.mjs'

// Sign-up restrictions (ADR-1190): which addresses may create accounts.
// GoTrue calls its before-user-created hook, outside its transactions, for
// every self-service sign-up: email and phone, magic links and codes, OAuth
// providers, SSO, anonymous users, Web3 and GoTrue's own invites. Users the
// app's operator creates or imports through the admin API are not checked.
// The rules: blocked domains (and their subdomains), throwaway domains, an
// allowlist of domains, and access by invitation or by waitlist.

// covered is true when the domain or one of its parents is in the set.
function covered(domain, set) {
  for (let d = domain; d.includes('.'); d = d.slice(d.indexOf('.') + 1)) if (set.has(d)) return true
  return false
}

let disposableDomains = null
export function disposableDomain(domain) {
  disposableDomains ??= new Set(readFileSync(new URL('./disposable-domains.txt', import.meta.url), 'utf8')
    .split('\n').map(line => line.trim()).filter(line => line && !line.startsWith('#')))
  return covered(domain, disposableDomains)
}

const refuse = code => ({ status: 403, code })

// signupRefusal answers the hook for one user GoTrue is about to create:
// null lets it, otherwise { status, code } refuses with a stable code.
// policy is null when the app restricts nothing but organizations may
// connect their own identity providers.
export async function signupRefusal(pool, policy, payload) {
  const user = payload?.user
  if (!user || typeof user !== 'object') throw new HTTPError(400, 'invalid_hook_payload')
  const email = typeof user.email === 'string' && user.email.includes('@') ? user.email.toLowerCase() : null
  // GoTrue takes whatever address a SAML identity provider asserts as
  // confirmed. One the operator registered vouches for its people; one an
  // organization connected itself (sql/0023) only for the domains it
  // verified, and its people follow the app's access rules like anyone.
  const idp = /^sso:([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$/i.exec(String(user.app_metadata?.provider ?? ''))?.[1]
  let operatorIdP = false
  if (idp) {
    const { rows: [rules] } = await pool.query('SELECT self_service, in_domain FROM gregale_auth.sso_signup_rules($1, $2)', [idp, email ? email.slice(email.lastIndexOf('@') + 1) : ''])
    if (rules.self_service && !rules.in_domain) return refuse('sso_email_domain_not_allowed')
    operatorIdP = !rules.self_service
  }
  if (!policy) return null
  // Phone numbers and anonymous users carry no address to check, so a
  // domain allowlist or restricted access refuses them.
  if (!email) return policy.allowed || policy.access !== 'open' ? refuse('signup_restricted') : null
  const domain = email.slice(email.lastIndexOf('@') + 1)
  if (covered(domain, policy.blocked)) return refuse('email_domain_blocked')
  if (policy.disposable && disposableDomain(domain)) return refuse('disposable_email')
  if (policy.allowed && !policy.allowed.has(domain)) return refuse('email_domain_not_allowed')
  if (policy.access === 'open' || operatorIdP) return null
  const { rows: [{ allowed }] } = await pool.query(`SELECT EXISTS (SELECT 1 FROM gregale_auth.signup_access WHERE email = $1 AND approved_at IS NOT NULL)
      OR EXISTS (SELECT 1 FROM gregale_auth.invitations WHERE email = $1 AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > now()) AS allowed`, [email])
  if (allowed) return null
  if (policy.access === 'invite') return refuse('signup_invitation_required')
  // The waitlist keeps one request per address and stops growing when full;
  // GoTrue's sign-up rate limits and CAPTCHA stand in front of it.
  const { rowCount } = await pool.query(`INSERT INTO gregale_auth.signup_access (email, requested_at)
      SELECT $1, now() WHERE (SELECT count(*) FROM gregale_auth.signup_access WHERE approved_at IS NULL) < $2
      ON CONFLICT (email) DO NOTHING`, [email, limits.waitlistPending])
  if (rowCount === 0 && !(await pool.query('SELECT 1 FROM gregale_auth.signup_access WHERE email = $1', [email])).rowCount) return refuse('waitlist_full')
  return refuse('waitlisted')
}

const address = /^[^\s@<>"]+@[^\s@<>"]+\.[^\s@<>"]+$/

// signupsRoute serves /admin/v1/signups: the waitlist and approved
// addresses, approving (with the signup_approved email when a provider is
// set up) and removing them. helpers are admin.mjs's paging functions.
export async function signupsRoute({ pool, mailer, external, app, pageParams, likePattern }, method, parts, url, body) {
  if (parts.length === 1 && method === 'GET') {
    const { page, perPage, search } = pageParams(url.searchParams)
    const status = url.searchParams.get('status') ?? 'all'
    if (!['pending', 'approved', 'all'].includes(status)) throw new HTTPError(400, 'invalid_status')
    const { rows } = await pool.query(`SELECT s.email, s.requested_at, s.approved_at, s.notified_at,
        EXISTS (SELECT 1 FROM auth.users u WHERE lower(u.email) = s.email) AS signed_up, count(*) OVER ()::int AS total
      FROM gregale_auth.signup_access s
      WHERE ($1 = 'all' OR ($1 = 'pending') = (s.approved_at IS NULL)) AND ($2 = '' OR s.email LIKE $3 ESCAPE '\\')
      ORDER BY coalesce(s.requested_at, s.approved_at) DESC, s.email LIMIT $4 OFFSET $5`, [status, search.toLowerCase(), likePattern(search.toLowerCase()), perPage, (page - 1) * perPage])
    return [200, { signups: rows.map(({ total, ...row }) => row), total: rows[0]?.total ?? 0, page, per_page: perPage }]
  }
  if (parts.length === 2 && parts[1] === 'approve' && method === 'POST') {
    const emails = body?.emails
    if (!Array.isArray(emails) || emails.length === 0 || emails.length > limits.signupApproveBatch ||
        emails.some(email => typeof email !== 'string' || email.length > 320 || !address.test(email))) throw new HTTPError(400, 'invalid_emails')
    if (body.notify !== undefined && typeof body.notify !== 'boolean') throw new HTTPError(400, 'invalid_notify')
    const { rows } = await pool.query(`INSERT INTO gregale_auth.signup_access (email, approved_at)
        SELECT DISTINCT lower(e), now() FROM unnest($1::text[]) AS e
        ON CONFLICT (email) DO UPDATE SET approved_at = coalesce(gregale_auth.signup_access.approved_at, now())
        RETURNING email, notified_at`, [emails])
    let notified = 0
    if (mailer && body.notify !== false) {
      const template = templateFor(await loadEmailTemplates(pool), 'signup_approved')
      for (const row of rows.filter(row => row.notified_at === null)) {
        await mailer.send(renderEmail(row.email, template, { app, email: row.email, link: `${external}/ui/sign-up` }), `signup-approved-${row.email}`)
        await pool.query('UPDATE gregale_auth.signup_access SET notified_at = now() WHERE email = $1', [row.email])
        notified++
      }
    }
    return [200, { approved: rows.length, notified }]
  }
  if (parts.length === 2 && method === 'DELETE') {
    let email
    try { email = decodeURIComponent(parts[1]).toLowerCase() } catch { throw new HTTPError(400, 'invalid_email') }
    const { rowCount } = await pool.query('DELETE FROM gregale_auth.signup_access WHERE email = $1', [email])
    if (!rowCount) throw new HTTPError(404, 'not_found')
    return [204, undefined]
  }
  throw new HTTPError(404, 'not_found')
}
