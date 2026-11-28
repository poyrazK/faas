import { addressBucket, budgetKey, goLower } from './attempts.mjs'
import { limits } from './config.mjs'
import { loadEmailTemplates, renderEmail, templateFor } from './email.mjs'
import { normalizePhone } from './sms.mjs'

// Sign-in networks and devices (ADR-1190, sql/0025). After each sign-in the
// front records, per user, the network the tokens went to (an IPv4 address
// or IPv6 /64) and the kind of device (its user agent without version
// numbers), as digests only. Two things read them:
//  - trusted networks: a password sign-in from a network the account used
//    in the last limits.trustedNetworkDays passes even while the account's
//    failure budget is spent (attempts.mjs), so a stranger guessing from
//    elsewhere cannot lock the owner out;
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

// sessionFromAnswer reads who signed in from GoTrue's token answer.
export function sessionFromAnswer(answer) {
  const user = answer?.user
  if (typeof answer?.access_token !== 'string' || typeof user?.id !== 'string' || !uuid.test(user.id)) return null
  return { userId: user.id.toLowerCase(), email: typeof user.email === 'string' && user.email !== '' ? user.email : null }
}

// sessionFromLocation reads who signed in from a redirect that hands the
// browser its tokens in the fragment (implicit flow, after an email link or
// a provider): GoTrue issued the token on loopback, so its payload is read
// without being verified again.
export function sessionFromLocation(location) {
  const hash = String(location ?? '').split('#')[1]
  if (!hash) return null
  const token = new URLSearchParams(hash).get('access_token')
  if (!token) return null
  let payload
  try { payload = JSON.parse(Buffer.from(token.split('.')[1], 'base64url').toString('utf8')) } catch { return null }
  if (typeof payload?.sub !== 'string' || !uuid.test(payload.sub)) return null
  return { userId: payload.sub.toLowerCase(), email: typeof payload.email === 'string' && payload.email !== '' ? payload.email : null }
}

const networkKey = (userId, address) => budgetKey('sign-in-network', userId, addressBucket(address))
const deviceKey = (userId, userAgent) => budgetKey('sign-in-device', userId, deviceKind(userAgent))

export function createSignInSecurity({ pool, mailer = null, alerts = true, app, templates = () => loadEmailTemplates(pool), log = console.error, now = () => new Date() }) {
  return {
    // trusted reports whether an account a password sign-in names (as
    // signInSubject writes it) signed in from this network recently.
    // GoTrue finds the account as it does for a password sign-in: by
    // lowercased email among users who are not SSO users, or by phone.
    async trusted(subject, address) {
      const [kind, value] = [subject.slice(0, subject.indexOf(':')), subject.slice(subject.indexOf(':') + 1)]
      const { rows } = kind === 'email'
        ? await pool.query('SELECT id FROM auth.users WHERE lower(email) = $1 AND is_sso_user = false LIMIT 10', [goLower(value)])
        : await pool.query('SELECT id FROM auth.users WHERE phone = $1 LIMIT 10', [normalizePhone(value)])
      if (rows.length === 0) return false
      const { rowCount } = await pool.query(`SELECT 1 FROM gregale_auth.sign_in_seen
        WHERE key = ANY($1) AND last_seen > now() - make_interval(days => $2) LIMIT 1`, [rows.map(row => networkKey(String(row.id), address)), limits.trustedNetworkDays])
      return rowCount > 0
    },
    // record notes a sign-in, refreshing each record at most once per
    // limits.signInSeenRefreshSeconds, and sends the new-device alert when
    // alert is set (not for a refreshed session).
    async record({ userId, email, address, userAgent, alert = true }) {
      const network = networkKey(userId, address), device = deviceKey(userId, userAgent)
      const { rows: [seen] } = await pool.query(`SELECT
          coalesce(bool_or(last_seen > now() - make_interval(days => $4)), false) AS known,
          coalesce(bool_or(key = $2 AND last_seen > now() - make_interval(days => $4)), false) AS network_seen,
          coalesce(bool_or(key = $3 AND last_seen > now() - make_interval(days => $4)), false) AS device_seen,
          coalesce(bool_or(key = $2 AND last_seen > now() - make_interval(secs => $5)), false)
            AND coalesce(bool_or(key = $3 AND last_seen > now() - make_interval(secs => $5)), false) AS fresh
        FROM gregale_auth.sign_in_seen WHERE user_id = $1`, [userId, network, device, limits.newDeviceDays, limits.signInSeenRefreshSeconds])
      if (!seen.fresh) {
        await pool.query(`INSERT INTO gregale_auth.sign_in_seen AS s (key, user_id) VALUES ($1, $3), ($2, $3)
          ON CONFLICT (key) DO UPDATE SET last_seen = now() WHERE s.last_seen < now() - make_interval(secs => $4)`, [network, device, userId, limits.signInSeenRefreshSeconds])
      }
      if (!alert || !alerts || !mailer || !email || !seen.known || seen.network_seen || seen.device_seen) return false
      const at = now()
      const message = renderEmail(email, templateFor(await templates(), 'new_device'), {
        app, email, device: deviceLabel(userAgent), network: addressBucket(address), time: `${at.toISOString().slice(0, 16).replace('T', ' ')} UTC`,
      })
      // One alert per device kind and day, however many sign-ins race.
      await mailer.send(message, `new-device-${userId}-${device.toString('hex').slice(0, 16)}-${at.toISOString().slice(0, 10)}`)
      log(JSON.stringify({ level: 'info', event: 'new_device_alert_sent', user_id: userId }))
      return true
    },
  }
}
