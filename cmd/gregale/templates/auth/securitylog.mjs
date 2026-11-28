// Security events (ADR-1190, sql/0026): what the front's sign-in protection
// did, for the owner to read. GoTrue's audit log records sign-ins, but not
// wrong passwords, holds, the passes past a spent account budget, message
// limits or new-device alerts. Alike events share a row whose count grows
// (dedupe), so an attack grows a count, not the table. Recording never fails
// or delays a request: an error is logged and the request goes on.
import { createHash, createHmac } from 'node:crypto'
import { addressBucket } from './attempts.mjs'
import { HTTPError } from './authz.mjs'
import { limits } from './config.mjs'

export const securityKinds = Object.freeze(['password_failed', 'code_failed', 'held', 'captcha_pass', 'trusted_pass', 'message_limited', 'new_device_alert'])

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

// networkLabel names an address's network: its IPv4 /24 or IPv6 /48, the
// latter in the standard compressed form (RFC 5952), so a filter typed as
// people write networks matches.
export function networkLabel(address) {
  const bucket = addressBucket(address)
  if (bucket.endsWith('::/64')) {
    const groups = [...bucket.slice(0, -'::/64'.length).split(':').slice(0, 3).map(group => Number.parseInt(group, 16)), 0, 0, 0, 0, 0]
    if (groups.some(Number.isNaN)) return null
    return `${compressIPv6(groups)}/48`
  }
  const parts = bucket.split('.')
  if (parts.length === 4 && parts.every(part => /^[0-9]{1,3}$/.test(part))) return `${parts.slice(0, 3).join('.')}.0/24`
  return null
}

// compressIPv6 writes eight groups with the longest run of two or more zero
// groups as '::'.
function compressIPv6(groups) {
  let best = -1, bestLength = 1
  for (let i = 0; i < 8;) {
    if (groups[i] !== 0) { i++; continue }
    let j = i
    while (j < 8 && groups[j] === 0) j++
    if (j - i > bestLength) { best = i; bestLength = j - i }
    i = j
  }
  const hex = groups.map(group => group.toString(16))
  if (best < 0) return hex.join(':')
  return `${hex.slice(0, best).join(':')}::${hex.slice(best + bestLength).join(':')}`
}

// networkFilter reads a network an owner typed: an address, an IPv4 /24 or
// an IPv6 /48, in any spelling, becomes the label it is stored under; any
// other prefix is refused (null), since events are stored by /24 and /48.
export function networkFilter(text) {
  const [address, prefix, extra] = text.split('/')
  if (extra !== undefined) return null
  const label = networkLabel(address)
  if (!label) return null
  if (prefix !== undefined && prefix !== (label.includes(':') ? '48' : '24')) return null
  return label
}

// addressDigest groups requests from one address without storing it: a
// digest keyed by a value derived from the app's JWT secret, so it cannot be
// reversed by hashing every address. Rotating the secret starts new groups.
export function addressDigester(secret) {
  const key = createHmac('sha256', String(secret)).update('gregale-auth-security-event-address').digest()
  return address => {
    const value = String(address ?? '').trim().toLowerCase().replace(/^\[(.*)\]$/, '$1').split('%')[0]
    if (!value) return null
    return createHmac('sha256', key).update(value).digest().subarray(0, 16)
  }
}

// subjectParts splits a budget subject ('email:…', 'phone:…', 'user:<id>')
// into what finds its user. GoTrue keeps emails lower-cased and phone numbers
// without '+', as attempts.mjs and sms.mjs normalize them.
function subjectParts(subject) {
  if (typeof subject !== 'string') return [null, null]
  const at = subject.indexOf(':')
  return at < 0 ? [null, null] : [subject.slice(0, at), subject.slice(at + 1)]
}

const insertStatement = `INSERT INTO gregale_auth.security_events AS e (kind, user_id, network, address_hash, detail, dedupe, budget_key, count)
  VALUES ($1,
    CASE WHEN $2::uuid IS NOT NULL THEN (SELECT id FROM auth.users WHERE id = $2::uuid)
      WHEN $3 = 'email' THEN (SELECT id FROM auth.users WHERE email = $4 AND NOT is_sso_user LIMIT 1)
      WHEN $3 = 'phone' THEN (SELECT id FROM auth.users WHERE phone = $4 LIMIT 1) END,
    $5, $6, $7::jsonb,
    COALESCE($8, sha256($9::bytea || $10::bytea || convert_to((SELECT window_ends_at FROM gregale_auth.attempt_budgets WHERE key = $9)::text, 'UTF8')),
      sha256($9::bytea || $10::bytea || convert_to(date_trunc('minute', now())::text, 'UTF8'))),
    $9, $11)
  ON CONFLICT (dedupe) DO UPDATE SET count = e.count + EXCLUDED.count, last_at = now(), detail = EXCLUDED.detail`

// createSecurityLog records events. record(kind, { userId, subject, address,
// detail, alike, hold }): userId or subject names the user (an unknown
// account leaves it empty); alike lists what makes events one row, within a
// minute; hold is the key of the attempt budget a refusal spent, which makes
// the refusals by it of one account from one network, until it reopens, one
// row (an address's budget refuses many accounts; an account's, many
// networks); if the budget's row is already gone when the write runs (a
// success cleared it, or it was pruned), its refusals group by minute
// instead. A row keeps the digest of the first address it saw.
//
// One writer at a time drains a queue, so recording takes at most one of
// the front's pool connections and never waits on its own row locks; alike
// events waiting in the queue merge into one write with their count, so an
// attack grows a number, not the queue. Past limits.securityEventsQueued
// waiting rows an event is dropped, and the number dropped is logged at most
// once a minute.
export function createSecurityLog({ pool, secret, log = console.error, now = Date.now }) {
  const digest = addressDigester(secret)
  const queue = new Map()
  let writing = null, dropped = 0, reported = -Infinity, unique = 0
  const report = () => {
    if (!dropped || now() - reported < 60000) return
    log(JSON.stringify({ level: 'warning', event: 'security_events_dropped', count: dropped }))
    dropped = 0
    reported = now()
  }
  const drain = async () => {
    while (queue.size) {
      const [id, entry] = queue.entries().next().value
      queue.delete(id)
      try {
        await pool.query(insertStatement, [...entry.args, entry.count])
      } catch (err) {
        log(JSON.stringify({ level: 'error', event: 'security_event_failed', kind: entry.args[0], sqlstate: err?.code ?? null }))
      }
      entry.done()
    }
    writing = null
    report()
  }
  return {
    record(kind, { userId = null, subject = null, address = null, detail = {}, alike = null, hold = null } = {}) {
      let [type, value] = subjectParts(subject)
      let user = typeof userId === 'string' && uuid.test(userId) ? userId.toLowerCase() : null
      // A user:<id> subject names its user directly.
      if (!user && type === 'user' && uuid.test(value)) user = value.toLowerCase()
      if (type === 'user') [type, value] = [null, null]
      const network = address ? networkLabel(address) : null
      const dedupe = alike ? createHash('sha256').update(JSON.stringify([kind, ...alike, Math.floor(now() / 60000)])).digest() : null
      const holder = hold ? createHash('sha256').update(JSON.stringify([kind, user ?? subject, network])).digest() : null
      const id = dedupe ? dedupe.toString('hex') : hold ? `${hold.toString('hex')}:${holder.toString('hex')}` : `once:${unique++}`
      // A merged event shares its entry's one promise, so an attack grows a
      // count, not the memory held for it.
      let entry = queue.get(id)
      if (entry) {
        entry.count++
        entry.args[6] = JSON.stringify(detail)
        return entry.written
      }
      if (queue.size >= limits.securityEventsQueued) {
        dropped++
        report()
        return Promise.resolve()
      }
      entry = { args: [kind, user, type, value, network, address ? digest(address) : null, JSON.stringify(detail), dedupe, hold, holder], count: 1 }
      entry.written = new Promise(resolve => { entry.done = resolve })
      queue.set(id, entry)
      writing ??= Promise.resolve().then(drain)
      return entry.written
    },
    // settled waits for the queue to be written (tests and shutdown).
    settled: () => writing ?? Promise.resolve(),
  }
}

// securityEvents answers GET /admin/v1/security-events: newest first,
// overall or for one user, kind or network. Pages carry no total.
export async function securityEvents(query, params) {
  const page = Number(params.get('page') ?? 1), perPage = Number(params.get('per_page') ?? 50)
  if (!Number.isInteger(page) || page < 1 || page > 100000) throw new HTTPError(400, 'invalid_page')
  if (!Number.isInteger(perPage) || perPage < 1 || perPage > limits.adminPageSize) throw new HTTPError(400, 'invalid_per_page')
  const user = params.get('user') ?? '', kind = params.get('kind') ?? '', network = params.get('network') ?? ''
  if (user && !uuid.test(user)) throw new HTTPError(400, 'invalid_id')
  if (kind && !securityKinds.includes(kind)) throw new HTTPError(400, 'invalid_kind')
  const filter = network && network.length <= 64 && /^[0-9a-f.:/[\]]+$/i.test(network) ? networkFilter(network) : null
  if (network && !filter) throw new HTTPError(400, 'invalid_network')
  const rows = await query(`SELECT e.id::text AS id, e.kind, e.user_id, u.email, u.phone, e.network, encode(e.address_hash, 'hex') AS address_id,
      e.detail, e.count, e.first_at, e.last_at
    FROM gregale_auth.security_events e LEFT JOIN auth.users u ON u.id = e.user_id
    WHERE ($1::uuid IS NULL OR e.user_id = $1::uuid) AND ($2 = '' OR e.kind = $2) AND ($3 = '' OR e.network = $3)
    ORDER BY e.last_at DESC, e.id DESC LIMIT $4 OFFSET $5`, [user ? user.toLowerCase() : null, kind, network ? filter : '', perPage + 1, (page - 1) * perPage])
  return { entries: rows.slice(0, perPage), more: rows.length > perPage, page, per_page: perPage }
}

// activeHolds lists a user's holds that still refuse: the hold's budget is
// still spent and its window open. A success from the held address clears
// that address's hold early.
export async function activeHolds(query, user) {
  return query(`SELECT DISTINCT ON (e.budget_key) e.detail->>'budget' AS budget, e.network, b.window_ends_at AS until
    FROM gregale_auth.security_events e JOIN gregale_auth.attempt_budgets b ON b.key = e.budget_key
    WHERE e.user_id = $1 AND e.kind IN ('held', 'message_limited') AND b.window_ends_at > now()
      AND b.failures >= (e.detail->>'limit')::int
    ORDER BY e.budget_key, e.last_at DESC`, [user])
}
