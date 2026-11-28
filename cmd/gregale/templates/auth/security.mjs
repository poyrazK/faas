import { addressBucket, budgetKey, goLower } from './attempts.mjs'
import { limits } from './config.mjs'
import { loadEmailTemplates, renderEmail, templateFor } from './email.mjs'
import { normalizePhone } from './sms.mjs'

// Sign-in networks and devices (ADR-1190, sql/0025). After each sign-in the
// front records, per user, the network the tokens went to (an IPv4 address
// or IPv6 /64) and the kind of device (its user agent without version
// numbers), as digests only. Two things read them:
//  - trusted networks: a password sign-in, or a message request, from a
//    network the account used in the last limits.trustedNetworkDays passes
//    even while the account's failure or message budget is spent
//    (attempts.mjs, sends.mjs), so a stranger cannot lock the owner out. A
//    network more than limits.trustedNetworkMaxAccounts accounts signed in
//    from in that time (an app's own server, a shared egress) is trusted
//    for none of them, and a sign-in only a redirect showed (an email link
//    scanner may follow one) records the network without trusting it;
//  - new-device alerts: a sign-in from both a network and a device kind the
//    account has not used in limits.newDeviceDays emails the person, when
//    the app sends email and the owner has not turned alerts off. A first
//    sign-in, or one after a long absence, alerts no one: there is nothing
//    to compare it with.

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

// deviceKind is a user agent without version numbers, so a browser update
// is not a new device.
export function deviceKind(userAgent) {
  return String(userAgent ?? '').replace(/[0-9]+/g, '').replace(/\s+/g, ' ').trim().slice(0, 512)
}

// deviceLabel names a device for the alert email: a browser and system the
// person would recognise, or the start of what the client sent.
export function deviceLabel(userAgent) {
  const ua = String(userAgent ?? '')
  const browser = [[/Edg(e|A|iOS)?\//, 'Edge'], [/OPR\/|Opera/, 'Opera'], [/Firefox\/|FxiOS/, 'Firefox'], [/Chrome\/|CriOS/, 'Chrome'], [/Safari\//, 'Safari']].find(([pattern]) => pattern.test(ua))?.[1]
  const system = [[/iPhone|iPad|iPod/, 'iOS'], [/Android/, 'Android'], [/Windows/, 'Windows'], [/Mac OS X|Macintosh/, 'macOS'], [/CrOS/, 'ChromeOS'], [/Linux/, 'Linux']].find(([pattern]) => pattern.test(ua))?.[1]
  if (browser && system) return `${browser} on ${system}`
  if (browser || system) return browser ?? system
  const raw = ua.replace(/[\x00-\x1f\x7f]+/g, ' ').trim()
  return raw === '' ? 'an unknown device' : raw.slice(0, 80)
}

// tokenPayload reads a token GoTrue issued on loopback, without verifying
// it again.
function tokenPayload(token) {
  try { return JSON.parse(Buffer.from(String(token).split('.')[1], 'base64url').toString('utf8')) } catch { return null }
}

// A session support staff opened as a user (impersonation.mjs) is not the
// person signing in: it is never recorded.
const impersonated = payload => typeof payload?.act?.sub === 'string' && payload.act.sub.startsWith('admin:')

// serverAgent reports whether a request comes from a server, not a
// person's device: an HTTP library's user agent, or none. An app's server
// that exchanges a code or signs someone in on their behalf is not their
// device or network, so such sign-ins are not recorded (no trust, no
// alert).
export function serverAgent(userAgent) {
  const ua = String(userAgent ?? '').trim()
  return ua === '' || /^(node|undici|node-fetch|axios|got|deno|bun|python|aiohttp|httpx|go-http-client|curl|wget|java|apache-httpclient|ruby|faraday|guzzlehttp|php|rust|reqwest|dart:io|supabase-js-node)\b/i.test(ua)
}

// sessionFromAnswer reads who signed in from GoTrue's token answer.
export function sessionFromAnswer(answer) {
  const user = answer?.user
  if (typeof answer?.access_token !== 'string' || typeof user?.id !== 'string' || !uuid.test(user.id)) return null
  if (impersonated(tokenPayload(answer.access_token))) return null
  return { userId: user.id.toLowerCase(), email: typeof user.email === 'string' && user.email !== '' ? user.email : null }
}

// sessionFromLocation reads who signed in from a redirect that hands the
// browser its tokens in the fragment (implicit flow, after an email link or
// a provider).
export function sessionFromLocation(location) {
  const hash = String(location ?? '').split('#')[1]
  if (!hash) return null
  const token = new URLSearchParams(hash).get('access_token')
  const payload = token ? tokenPayload(token) : null
  if (typeof payload?.sub !== 'string' || !uuid.test(payload.sub) || impersonated(payload)) return null
  return { userId: payload.sub.toLowerCase(), email: typeof payload.email === 'string' && payload.email !== '' ? payload.email : null }
}

const networkKey = (userId, address) => budgetKey('sign-in-network', userId, addressBucket(address))
const networkOnly = address => budgetKey('sign-in-network', addressBucket(address))
const deviceKey = (userId, userAgent) => budgetKey('sign-in-device', userId, deviceKind(userAgent))

export function createSignInSecurity({ pool, mailer = null, alerts = true, app, templates = () => loadEmailTemplates(pool), log = console.error, now = () => new Date() }) {
  return {
    // trusted reports whether an account a password sign-in or message
    // names (as signInSubject writes it: email:, phone:, or user: for the
    // signed-in user) signed in from this network recently, by a sign-in
    // the front saw hand over its tokens, from a network few accounts
    // share. GoTrue finds the account as it does for a password sign-in: by
    // lowercased email among users who are not SSO users, or by phone.
    async trusted(subject, address) {
      const at = subject.indexOf(':'), kind = subject.slice(0, at), value = subject.slice(at + 1)
      let ids
      if (kind === 'user') ids = uuid.test(value) ? [value.toLowerCase()] : []
      else {
        const { rows } = kind === 'email'
          ? await pool.query('SELECT id FROM auth.users WHERE lower(email) = $1 AND is_sso_user = false LIMIT 10', [goLower(value)])
          : await pool.query('SELECT id FROM auth.users WHERE phone = $1 LIMIT 10', [normalizePhone(value)])
        ids = rows.map(row => String(row.id))
      }
      if (ids.length === 0) return false
      const { rowCount } = await pool.query(`SELECT 1 FROM gregale_auth.sign_in_seen
        WHERE key = ANY($1) AND trusted AND last_seen > now() - make_interval(days => $2)
          AND (SELECT count(DISTINCT user_id) FROM gregale_auth.sign_in_seen
            WHERE network = $3 AND last_seen > now() - make_interval(days => $2)) <= $4
        LIMIT 1`, [ids.map(id => networkKey(id, address)), limits.trustedNetworkDays, networkOnly(address), limits.trustedNetworkMaxAccounts])
      return rowCount > 0
    },
    // record notes a sign-in, refreshing each record at most once per
    // limits.signInSeenRefreshSeconds, and sends the new-device alert when
    // alert is set. trust says whether the sign-in shows the person on this
    // network: true for a token grant, false for a session only a redirect
    // showed (a mail scanner may follow one; recorded untrusted), 'known'
    // for a refreshed session (a stolen refresh token must not make its
    // network trusted), which trusts only a network the account is trusted
    // on, or one where a redirect handed this device a session, and is
    // otherwise not recorded. Only trusted records count as seen when
    // deciding to alert. A failed alert is logged, never thrown: the
    // sign-in is still recorded.
    async record({ userId, email, address, userAgent, alert = true, trust = true }) {
      const network = networkKey(userId, address), device = deviceKey(userId, userAgent)
      const { rows: [seen] } = await pool.query(`SELECT
          coalesce(bool_or(trusted AND last_seen > now() - make_interval(days => $4)), false) AS known,
          coalesce(bool_or(key = $2 AND trusted AND last_seen > now() - make_interval(days => $4)), false) AS network_seen,
          coalesce(bool_or(key = $3 AND trusted AND last_seen > now() - make_interval(days => $4)), false) AS device_seen,
          coalesce(bool_or(key = $2 AND last_seen > now() - make_interval(days => $4)), false) AS network_recorded,
          coalesce(bool_or(key = $3 AND last_seen > now() - make_interval(days => $4)), false) AS device_recorded,
          coalesce(bool_or(key = $2 AND trusted AND last_seen > now() - make_interval(secs => $5)), false)
            AND coalesce(bool_or(key = $3 AND trusted AND last_seen > now() - make_interval(secs => $5)), false) AS fresh_trusted,
          coalesce(bool_or(key = $2 AND last_seen > now() - make_interval(secs => $5)), false)
            AND coalesce(bool_or(key = $3 AND last_seen > now() - make_interval(secs => $5)), false) AS fresh
        FROM gregale_auth.sign_in_seen WHERE user_id = $1`, [userId, network, device, limits.newDeviceDays, limits.signInSeenRefreshSeconds])
      const trusted = trust === true || (trust === 'known' && (seen.network_seen || (seen.network_recorded && seen.device_recorded)))
      const written = !(trust === 'known' && !trusted) && !(trusted ? seen.fresh_trusted : seen.fresh)
      if (written) {
        // A record is refreshed hourly, or at once when a trusted sign-in
        // follows untrusted ones; trust is never taken back.
        await pool.query(`INSERT INTO gregale_auth.sign_in_seen AS s (key, user_id, network, trusted) VALUES ($1, $3, $5, $6), ($2, $3, NULL, $6)
          ON CONFLICT (key) DO UPDATE SET last_seen = now(), trusted = s.trusted OR EXCLUDED.trusted
          WHERE s.last_seen < now() - make_interval(secs => $4) OR (EXCLUDED.trusted AND NOT s.trusted)`, [network, device, userId, limits.signInSeenRefreshSeconds, networkOnly(address), trusted])
      }
      if (!alert || !alerts || !mailer || !email || !seen.known || seen.network_seen || seen.device_seen) return false
      try {
        return await this.alert({ userId, email, address, userAgent })
      } catch (err) {
        // The network stays recorded (trust must not wait on email); the
        // device is put back as it was, so the next sign-in from it alerts.
        log(JSON.stringify({ level: 'error', event: 'new_device_alert_failed', user_id: userId }))
        if (written) {
          await pool.query(seen.device_recorded ? 'UPDATE gregale_auth.sign_in_seen SET trusted = false WHERE key = $1' : 'DELETE FROM gregale_auth.sign_in_seen WHERE key = $1', [device]).catch(() => {})
        }
        return false
      }
    },
    // alert sends the new-device email once per device kind in
    // limits.newDeviceDays, however many sign-ins race or refresh: the first
    // to claim the marker (an attempt_budgets row, pruned with them) sends
    // it, whatever the email provider deduplicates. A failed send gives the
    // marker back and throws.
    async alert({ userId, email, address, userAgent }) {
      const at = now()
      const marker = budgetKey('new-device-alert', userId, deviceKind(userAgent))
      const { rowCount } = await pool.query(`INSERT INTO gregale_auth.attempt_budgets AS b (key, failures, window_ends_at)
        VALUES ($1, 1, now() + make_interval(days => $2))
        ON CONFLICT (key) DO UPDATE SET failures = 1, window_ends_at = EXCLUDED.window_ends_at WHERE b.window_ends_at <= now()`, [marker, limits.newDeviceDays])
      if (rowCount === 0) return false
      try {
        const message = renderEmail(email, templateFor(await templates(), 'new_device'), {
          app, email, device: deviceLabel(userAgent), network: addressBucket(address), time: `${at.toISOString().slice(0, 16).replace('T', ' ')} UTC`,
        })
        await mailer.send(message, `new-device-${userId}-${deviceKey(userId, userAgent).toString('hex').slice(0, 16)}-${at.toISOString().slice(0, 10)}`)
      } catch (err) {
        await pool.query('DELETE FROM gregale_auth.attempt_budgets WHERE key = $1', [marker]).catch(() => {})
        throw err
      }
      log(JSON.stringify({ level: 'info', event: 'new_device_alert_sent', user_id: userId }))
      return true
    },
  }
}
