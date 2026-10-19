import { randomUUID } from 'node:crypto'
import { limits } from './config.mjs'

// Owner usage telemetry and monthly caps (ADR 20261019102108). The runtime
// counts requests per UTC hour and reports deltas to the platform under its
// workload identity. Counts come from the customer's own VM, so they are
// owner telemetry, never billing records.

// The access settings the platform applies to this app's live reads without a
// user token (ADR 20261016151947): exposed schemas, the public-read allowlist and
// server-key hashes with their permissions. Never a key.
export function accessReport(config) {
  const name = entry => `${entry.schema}.${entry.name ?? entry.relation}`
  const permissions = p => ({ access: p.access, ...(p.relations ? { relations: p.relations.map(name) } : {}),
    ...(p.rpcs ? { rpcs: p.rpcs.map(name) } : {}), subjects: p.subjects })
  return { version: 1, schemas: [...config.schemas],
    ...(config.publicRead ? { public_read: config.publicRead.map(name) } : {}),
    ...(config.serverKeys ? { server_keys: config.serverKeys.map(k => ({ slot: k.slot, sha256: k.hash.toString('hex'), ...(k.permissions ? { permissions: permissions(k.permissions) } : {}) })) } : {}) }
}

const hourMs = 3600000
const emptyCounts = () => ({ user_requests: 0, anonymous_requests: 0, server_requests: 0, rejected_requests: 0, error_requests: 0, capped_requests: 0, rows_returned: 0, response_bytes: 0 })
const month = ms => new Date(ms).toISOString().slice(0, 7)
const nextMonth = ms => { const d = new Date(ms); return Date.UTC(d.getUTCFullYear(), d.getUTCMonth() + 1, 1) }
const track = Symbol('usageTrack')

// Rows the engine reported for a response: Content-Range "0-24/*" is 25 rows.
export function rowsFromContentRange(value) {
  const match = typeof value === 'string' ? /^(\d+)-(\d+)\//.exec(value) : null
  return match && Number(match[2]) >= Number(match[1]) ? Number(match[2]) - Number(match[1]) + 1 : 0
}

export function addResponseBytes(res, bytes) {
  if (res[track]) res[track].bytes += bytes
}

function instanceOf(token) {
  try {
    const claims = JSON.parse(Buffer.from(token.split('.')[1], 'base64url').toString('utf8'))
    return typeof claims.instance_id === 'string' && /^[A-Za-z0-9-]{1,64}$/.test(claims.instance_id) ? claims.instance_id : null
  } catch { return null }
}

// createUsage counts and reports one process's usage. Snapshot restores copy
// this process's memory into several instances. Counts are therefore
// reported within one interval, so little is pending when an idle app parks.
// A restore, seen as the wall clock jumping against the monotonic clock,
// drops pending counts rather than reporting them once per instance. An
// in-flight report keeps its id, which the platform applies once.
export function createUsage(config, { now = Date.now, monotonic = () => performance.now(), fetchImpl = fetch, nonce = randomUUID(), access = null } = {}) {
  if (!config) return null
  const pending = new Map()
  let inflight = null, sequence = 0, totals = null, timer = null, busy = false, offset = now() - monotonic(), stopped = false
  // Access settings go out after start, after a restore and hourly, so the
  // platform's copy follows restarts with new settings.
  let accessSentAt = null
  const accessDue = () => access !== null && (accessSentAt === null || now() - accessSentAt >= hourMs)

  const restored = () => {
    const current = now() - monotonic()
    if (Math.abs(current - offset) <= limits.usageRestoreJumpMs) return false
    offset = current
    pending.clear()
    totals = null
    accessSentAt = null
    return true
  }
  const schedule = delay => {
    if (stopped || timer) return
    timer = setTimeout(() => { timer = null; void report() }, delay)
    timer.unref?.()
  }
  const bucket = at => {
    const hour = Math.floor(at / hourMs) * hourMs
    let counts = pending.get(hour)
    if (!counts) {
      // Bounded memory: the oldest unreported hour goes first.
      if (pending.size >= limits.usageReportMaxHours) pending.delete(Math.min(...pending.keys()))
      counts = emptyCounts()
      pending.set(hour, counts)
    }
    return counts
  }
  const local = at => {
    const current = month(at), sum = { requests: 0, anonymous_requests: 0 }
    const add = (hour, c) => {
      if (month(hour) !== current) return
      sum.requests += c.user_requests + c.anonymous_requests + c.server_requests
      sum.anonymous_requests += c.anonymous_requests
    }
    for (const [hour, c] of pending) add(hour, c)
    for (const h of inflight?.hours ?? []) add(Date.parse(h.hour), h)
    return sum
  }

  function record(entry) {
    if (restored()) schedule(0)
    const counts = bucket(now())
    if (entry.capped) counts.capped_requests++
    else if (['user', 'anonymous', 'server'].includes(entry.access)) counts[`${entry.access}_requests`]++
    if (entry.status >= 500) counts.error_requests++
    else if (entry.status >= 400 && !entry.capped) counts.rejected_requests++
    counts.rows_returned += entry.rows
    counts.response_bytes += entry.bytes
    schedule(limits.usageReportIntervalMs)
  }

  // Refusal for an admitted access kind once a cap is reached, else null.
  function capReached(access) {
    const caps = config.caps
    if (!caps) return null
    if (restored()) schedule(0)
    const at = now()
    const base = totals && totals.month === month(at) ? totals : { requests: 0, anonymous_requests: 0 }
    if (!totals) schedule(0)
    const mine = local(at)
    const retryAfter = Math.max(1, Math.ceil((nextMonth(at) - at) / 1000))
    const check = (name, observed) => caps[name] !== null && observed >= caps[name] ? { name, limit: caps[name], observed, retryAfter } : null
    return check('requests', base.requests + mine.requests) ?? (access === 'anonymous' ? check('anonymous_requests', base.anonymous_requests + mine.anonymous_requests) : null)
  }

  async function report() {
    if (busy || stopped) return
    busy = true
    try {
      restored()
      if (!inflight && !pending.size && !accessDue() && (!config.caps || totals && totals.month === month(now()))) return
      const signal = AbortSignal.timeout(limits.usageReportMs)
      const identity = await fetchImpl(config.identity, { signal, redirect: 'error' })
      if (!identity.ok) { await identity.body?.cancel(); return }
      const body = await identity.json()
      if (body?.token_type !== 'Bearer' || typeof body.access_token !== 'string' || !body.access_token || body.access_token.length > limits.healthBytes) return
      const instance = instanceOf(body.access_token)
      if (!instance) return
      if (!inflight) {
        const cutoff = Math.floor(now() / hourMs) * hourMs - limits.usageReportMaxHours * hourMs
        const hours = [...pending].filter(([hour]) => hour > cutoff).sort(([a], [b]) => a - b).map(([hour, c]) => ({ hour: new Date(hour).toISOString(), ...c }))
        pending.clear()
        inflight = { report_id: `${instance}.${nonce}:${++sequence}`, hours, ...(accessDue() ? { access } : {}) }
      }
      const response = await fetchImpl(config.endpoint, { method: 'POST', redirect: 'error', signal,
        headers: { Authorization: `Bearer ${body.access_token}`, 'Content-Type': 'application/json' },
        body: JSON.stringify({ version: 1, ...inflight }) })
      if (response.ok) {
        const answer = await response.json()
        if (inflight.access) accessSentAt = now()
        inflight = null
        const mtd = answer?.month_to_date
        if (mtd && typeof mtd.month === 'string' && Number.isSafeInteger(mtd.requests) && Number.isSafeInteger(mtd.anonymous_requests)) totals = { month: mtd.month, requests: mtd.requests, anonymous_requests: mtd.anonymous_requests }
      } else {
        await response.body?.cancel()
        // A report the platform refuses cannot succeed on retry.
        if (response.status >= 400 && response.status < 500 && response.status !== 401 && response.status !== 429) inflight = null
      }
    } catch { /* Usage is best effort: never log tokens or platform errors. */ } finally {
      busy = false
      if (inflight || pending.size) schedule(limits.usageReportIntervalMs)
    }
  }

  // Counts one request when its response closes. Health checks never call this.
  function start(res) {
    const entry = { access: null, capped: false, bytes: 0 }
    res[track] = entry
    res.once('close', () => record({ ...entry, status: res.headersSent ? res.statusCode : 0, rows: rowsFromContentRange(res.getHeader('content-range')) }))
    return entry
  }

  if (config.caps || access !== null) schedule(0)
  return Object.freeze({ start, capReached, report, stop: () => { stopped = true; clearTimeout(timer) },
    flush: async () => { clearTimeout(timer); timer = null; await report() } })
}
