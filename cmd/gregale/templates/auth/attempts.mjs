import { createHash } from 'node:crypto'
import { limits } from './config.mjs'
import { normalizePhone } from './sms.mjs'

// Failed-attempt budgets (ADR-1190, sql/0024). GoTrue limits sign-ins per
// client address in each instance's memory, which a snapshot restore resets
// and other instances do not see, and it never counts per account. The
// front keeps these budgets in the database instead:
//  - password sign-in: per account and client address (one person typing
//    wrongly is held only at that address) and per account (guessing from
//    many addresses), counted when GoTrue answers invalid_credentials;
//  - second step: per user, counted when GoTrue answers
//    mfa_verification_failed for an authenticator, SMS or recovery code;
//  - service-account secrets: per client address (serviceaccounts.mjs).
// Each attempt is counted before GoTrue sees it (reserve), in one statement
// per budget, so a burst of parallel guesses cannot all pass a check made
// before any of them failed; an attempt over a budget answers 429 without
// reaching GoTrue. GoTrue's answer then settles it: a counted failure stays,
// a success clears the account's budgets, and anything else (a malformed
// request, an outage) is given back (release). Spending an account's budget from
// many addresses pauses password sign-in for it until the window ends;
// magic links, codes, providers and passkeys keep working.

export const budgets = Object.freeze({
  passwordAddress: { limit: limits.passwordFailuresPerAddress, seconds: limits.attemptWindowSeconds },
  passwordAccount: { limit: limits.passwordFailuresPerAccount, seconds: limits.attemptWindowSeconds },
  secondStep: { limit: limits.secondStepFailuresPerUser, seconds: limits.attemptWindowSeconds },
  serviceAccountClient: { limit: limits.serviceAccountTokensPerMinute, seconds: 60 },
  // Dynamic OAuth client registrations, counted whether or not they succeed:
  // the app holds at most limits.oauthClients, so one address must not be
  // able to take them all.
  oauthRegistration: { limit: limits.oauthRegistrationsPerAddress, seconds: limits.oauthRegistrationWindowSeconds },
})

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

export function budgetKey(name, ...subject) {
  return createHash('sha256').update(JSON.stringify([name, ...subject])).digest()
}

// createAttempts reads and writes budgets. check returns the seconds until
// the first spent budget among those given reopens, or 0.
export function createAttempts(pool) {
  return {
    async check(entries) {
      const { rows } = await pool.query(`SELECT key, failures, ceil(extract(epoch FROM window_ends_at - now()))::int AS wait
        FROM gregale_auth.attempt_budgets WHERE key = ANY($1) AND window_ends_at > now()`, [entries.map(entry => entry.key)])
      let wait = 0
      for (const row of rows) {
        const entry = entries.find(candidate => candidate.key.equals(row.key))
        if (entry && row.failures >= entry.budget.limit) wait = Math.max(wait, Math.max(1, row.wait))
      }
      return wait
    },
    async fail(entries) {
      for (const { key, budget } of entries) {
        await pool.query(`INSERT INTO gregale_auth.attempt_budgets AS b (key, failures, window_ends_at)
            VALUES ($1, 1, now() + make_interval(secs => $2))
          ON CONFLICT (key) DO UPDATE SET
            failures = CASE WHEN b.window_ends_at > now() THEN b.failures + 1 ELSE 1 END,
            window_ends_at = CASE WHEN b.window_ends_at > now() THEN b.window_ends_at ELSE now() + make_interval(secs => $2) END`, [key, budget.seconds])
      }
    },
    // reserve counts one attempt against every budget and returns the
    // seconds until the first one it overspent reopens, or 0.
    async reserve(entries) {
      let wait = 0
      for (const { key, budget } of entries) {
        const { rows: [row] } = await pool.query(`INSERT INTO gregale_auth.attempt_budgets AS b (key, failures, window_ends_at)
            VALUES ($1, 1, now() + make_interval(secs => $2))
          ON CONFLICT (key) DO UPDATE SET
            failures = CASE WHEN b.window_ends_at > now() THEN b.failures + 1 ELSE 1 END,
            window_ends_at = CASE WHEN b.window_ends_at > now() THEN b.window_ends_at ELSE now() + make_interval(secs => $2) END
          RETURNING failures, ceil(extract(epoch FROM window_ends_at - now()))::int AS wait`, [key, budget.seconds])
        if (row.failures > budget.limit) wait = Math.max(wait, Math.max(1, row.wait))
      }
      return wait
    },
    // release gives back an attempt reserve counted that was not a failure:
    // one statement, so a budget at two drops to one rather than being both
    // decremented and then deleted.
    async release(entries) {
      await pool.query(`WITH gone AS (
          DELETE FROM gregale_auth.attempt_budgets WHERE key = ANY($1) AND failures = 1 AND window_ends_at > now() RETURNING key)
        UPDATE gregale_auth.attempt_budgets SET failures = failures - 1
        WHERE key = ANY($1) AND failures > 1 AND window_ends_at > now()`, [entries.map(entry => entry.key)])
    },
    async clear(entries) {
      await pool.query('DELETE FROM gregale_auth.attempt_budgets WHERE key = ANY($1)', [entries.map(entry => entry.key)])
    },
  }
}

// signInSubject reads the account a password sign-in names, as GoTrue
// v2.197.0 would: Go's JSON decoding matches field names without regard to
// case, so more than one email or phone key is refused rather than guessed.
// GoTrue reads the grant's fields only as JSON, so a body the front cannot
// parse is refused here (throws 'invalid') rather than passed on unbudgeted.
// It returns null for a body that names no account, which GoTrue refuses,
// and throws 'ambiguous' for one that names it twice.
export function signInSubject(raw) {
  let body
  try { body = JSON.parse(raw.toString('utf8')) } catch { throw new Error('invalid') }
  if (!body || typeof body !== 'object' || Array.isArray(body)) throw new Error('invalid')
  const fields = name => Object.entries(body).filter(([key]) => key.toLowerCase() === name)
  const email = fields('email'), phone = fields('phone')
  if (email.length > 1 || phone.length > 1) throw new Error('ambiguous')
  if (typeof email[0]?.[1] === 'string' && email[0][1] !== '') return `email:${goLower(email[0][1])}`
  if (typeof phone[0]?.[1] === 'string' && phone[0][1] !== '') return `phone:${normalizePhone(phone[0][1])}`
  return null
}

// goLower lowercases as Go's strings.ToLower does, which GoTrue v2.197.0
// uses to find an account (LOWER(email) = strings.ToLower(input)): one code
// point at a time with the simple mapping. JavaScript's toLowerCase uses the
// full mapping, which turns U+0130 (İ) into two code points where Go gives
// "i", so "alİce@x" would otherwise sign in to alice@x with its own budget.
export function goLower(value) {
  let out = ''
  for (const char of value) out += char === 'İ' ? 'i' : char.toLowerCase()
  return out
}

// goQueryValue returns the first value of name in a query string as Go's
// url.ParseQuery (and so GoTrue's r.FormValue) reads it: a pair containing
// a semicolon, or with an invalid escape, is dropped rather than read. Node's
// URLSearchParams keeps both, so "grant_type=x;&grant_type=password" would
// otherwise read as "x;" here and "password" in GoTrue.
export function goQueryValue(query, name) {
  for (const pair of query.split('&')) {
    if (pair === '' || pair.includes(';')) continue
    const at = pair.indexOf('=')
    const [rawKey, rawValue] = at === -1 ? [pair, ''] : [pair.slice(0, at), pair.slice(at + 1)]
    let key, value
    try {
      key = decodeURIComponent(rawKey.replaceAll('+', ' '))
      value = decodeURIComponent(rawValue.replaceAll('+', ' '))
    } catch { continue }
    if (key === name) return value
  }
  return null
}

// passwordEntries are the budgets one password sign-in spends.
export function passwordEntries(subject, address) {
  return [
    { key: budgetKey('password-address', subject, address), budget: budgets.passwordAddress },
    { key: budgetKey('password-account', subject), budget: budgets.passwordAccount },
  ]
}

// secondStepEntries are the budgets a second-step verification spends,
// keyed by the user its token names. The token is GoTrue's to verify: a
// forged one is refused there and never counts.
export function secondStepEntries(claims) {
  return typeof claims?.sub === 'string' && uuid.test(claims.sub) ? [{ key: budgetKey('second-step', claims.sub.toLowerCase()), budget: budgets.secondStep }] : []
}

// guardedPath names the requests the front budgets: a password sign-in at
// the token endpoint, and second-step verification.
export function guardedPath(method, path) {
  if (method !== 'POST') return null
  const [raw, query = ''] = path.split('?')
  // GoTrue's router serves a path with trailing slashes as the path.
  const route = raw.replace(/\/+$/, '')
  if (route === '/token') {
    // GoTrue reads the grant from the query (r.FormValue), parsed as Go
    // parses it; the password grant's fields come from the JSON body.
    return goQueryValue(query, 'grant_type') === 'password' ? 'password' : null
  }
  return /^\/factors\/(recovery-codes|[0-9a-fA-F-]{36})\/verify$/.test(route) ? 'second-step' : null
}

// outcome reads GoTrue's answer: a counted failure, a success, or neither.
export function outcome(kind, status, body) {
  const code = body?.error_code
  if (status === 200) return 'success'
  if (kind === 'password' && status === 400 && code === 'invalid_credentials') return 'failure'
  if (kind === 'second-step' && status === 422 && code === 'mfa_verification_failed') return 'failure'
  return null
}
