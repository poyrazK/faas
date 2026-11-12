import http from 'node:http'
import { randomUUID } from 'node:crypto'
import pg from 'pg'
import { SignJWT } from 'jose'
import { limits, sqlDeadlineExpiry, UnsafeConfiguration } from './config.mjs'
import { runtimeFailure } from './logging.mjs'

// Change hooks (ADR-20261022121642). Owner triggers record each change of a
// hooked table in <schema>.gregale_change_events inside the writing
// transaction. The relay claims recorded changes in order through PostgREST,
// publishes them as app-scoped Gregale events under the runtime's workload
// identity and settles them: published events are deleted and rejected ones
// marked failed. One relay per database publishes at a time.
//
// Amendment 1: the relay reports delivery (waiting and rejected changes, the
// oldest waiting change, whether publications keep failing) to the platform,
// so owners see it without waking the API, and a platform cron may wake a
// parked API to drain changes other writers recorded meanwhile.
//
// Amendment 2: only production publishes. The platform refuses a stage,
// preview or cloned environment for good (403); its relay returns the claim
// and stops, and capture's backlog bound keeps that database's table finite.

export const changeEventsTable = 'gregale_change_events'
// The relay's drop note (amendment 3): when capture last left a change out.
export const changeDropsTable = 'gregale_change_drops'
// The relay's tables, never part of the API.
export const relayTables = Object.freeze([changeEventsTable, changeDropsTable])
// The drain route a drain schedule's cron calls (api.DataAPIChangeHooksDrainPath).
export const changeHooksDrainPath = '/__gregale/hooks/drain'

const operations = { insert: 'inserted', update: 'updated', delete: 'deleted' }
const eventKey = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/
const failureCode = /^[a-z0-9_]{1,64}$/

const changeEventsQuery = `SELECT c.relkind AS kind, c.relrowsecurity AS rls,
 has_table_privilege(current_user, c.oid, 'SELECT') AND has_table_privilege(current_user, c.oid, 'UPDATE') AND has_table_privilege(current_user, c.oid, 'DELETE') AS settles,
 (SELECT count(*) FROM pg_proc p WHERE p.pronamespace = c.relnamespace AND p.proname IN ('gregale_change_claim', 'gregale_change_settle')
   AND NOT p.prosecdef AND has_function_privilege(current_user, p.oid, 'EXECUTE'))::int AS functions,
 EXISTS (SELECT 1 FROM pg_proc p JOIN pg_namespace pn ON pn.oid = p.pronamespace
   WHERE pn.nspname = 'gregale_hooks' AND p.proname = 'capture' AND p.prosrc LIKE '%gregale:backlog-bound%') AS bounded
 FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = $1 AND c.relname = $2`

// unsafeChangeEvents explains why the catalog cannot serve the relay.
export function unsafeChangeEvents(row) {
  if (!row) return 'the gregale_change_events table is missing'
  if (row.kind !== 'r') return 'gregale_change_events is not a table'
  if (row.rls !== true) return 'gregale_change_events needs row-level security enabled'
  if (row.settles !== true) return 'the Data API login cannot settle gregale_change_events'
  if (row.functions !== 2) return 'gregale_change_claim and gregale_change_settle are missing or unsafe'
  return null
}

// checkChangeEvents refuses an unsafe installation and reports whether its
// capture has the backlog bound (amendment 2); one installed before it still
// works, and `gregale data-api hooks export --upgrade` adds the bound.
export async function checkChangeEvents(connection, schema) {
  const client = new pg.Client({ connectionString: connection, statement_timeout: limits.queryMs, connectionTimeoutMillis: limits.queryMs })
  await client.connect()
  try {
    const { rows } = await client.query(changeEventsQuery, [schema, changeEventsTable])
    const unsafe = unsafeChangeEvents(rows[0])
    if (unsafe) throw new UnsafeConfiguration(`Change hooks are not installed: ${unsafe}`)
    return { bounded: rows[0].bounded === true }
  } finally { await client.end() }
}

// validRow accepts a claimed row the relay can publish.
function validRow(row) {
  return row !== null && typeof row === 'object' && Number.isSafeInteger(row.id) && typeof row.event_key === 'string' && eventKey.test(row.event_key) &&
    Object.hasOwn(operations, row.op) && typeof row.relation_schema === 'string' && typeof row.relation_name === 'string' &&
    typeof row.captured_at === 'string' && !Number.isNaN(Date.parse(row.captured_at)) && row.row_key !== null && typeof row.row_key === 'object'
}

// changeEvent is the publication for one recorded change. It depends only on
// the row, so a republished change is identical and deduplicates.
export function changeEvent(row) {
  const data = { schema: row.relation_schema, table: row.relation_name, op: row.op, key: row.row_key, new: row.new_row ?? null, old: row.old_row ?? null,
    ...(Array.isArray(row.changed) ? { changed: row.changed } : {}), sequence: row.id, txid: row.txid, ...(row.truncated === true ? { truncated: true } : {}) }
  if (Buffer.byteLength(JSON.stringify(data)) > limits.changeEventDataBytes) {
    data.new = null
    data.old = null
    data.truncated = true
  }
  return { id: row.event_key, type: `${row.relation_schema}.${row.relation_name}.${operations[row.op]}`, time: row.captured_at, data,
    ...(row.schema_version ? { schema_version: row.schema_version } : {}) }
}

// pack takes leading events while the publication stays within its bounds.
export function pack(events) {
  const taken = []
  let bytes = Buffer.byteLength('{"version":1,"events":[]}')
  for (const event of events) {
    const size = Buffer.byteLength(JSON.stringify(event)) + (taken.length ? 1 : 0)
    if (taken.length >= limits.changeEventsBatch || (taken.length && bytes + size > limits.changeEventsBatchBytes)) break
    taken.push(event)
    bytes += size
  }
  return taken
}

// settlement turns per-event publication results into the claim's outcome. It
// stops at the first event that may succeed later: later events wait for it.
export function settlement(rows, results) {
  const published = [], failed = []
  for (const [index, row] of rows.entries()) {
    const result = Array.isArray(results) ? results[index] : null
    if (result?.index !== index) return { published, failed, retry: true }
    if (result.status === 'accepted' || result.status === 'duplicate') published.push(row.id)
    else if (result.status === 'rejected' && result.retryable === false) failed.push({ id: row.id, code: failureCode.test(result.problem?.code ?? '') ? result.problem.code : 'rejected' })
    else return { published, failed, retry: true }
  }
  return { published, failed, retry: false }
}

// createChangeRelay starts the relay. tryAcquire lends it a free engine slot;
// kick() asks for a prompt cycle after a successful write.
export function createChangeRelay(config, { upstreamPort, tryAcquire, fetchImpl = fetch, now = Date.now, monotonic = () => performance.now(), sleep = ms => new Promise(resolve => setTimeout(resolve, ms).unref?.()),
  setTimer = (work, delay) => setTimeout(work, delay).unref(), clearTimer = clearTimeout } = {}) {
  if (!config.changeEvents || config.changeEventsUnavailable) return null
  const settings = config.changeEvents
  const key = new TextEncoder().encode(config.secret.toString('base64url'))
  let relay = `relay.${randomUUID()}`, offset = now() - monotonic()
  let timer = null, dueAt = Infinity, busy = false, again = false, stopped = false
  let idle = limits.changeEventsIdleMinMs, retry = limits.changeEventsIdleMinMs
  // Delivery reports: the last one sent, whether the platform takes them, and
  // when this relay last published.
  let reportedAt = -Infinity, reported = null, reportsRefused = false, publishedAt = null, forceReport = true, retrying = false
  // The platform refused this runtime for good: not a production deployment.
  let refused = false
  const drains = new Set()

  // Snapshot restores copy this process; each copy takes a new identity so
  // copies never share a claim.
  const restored = () => {
    const current = now() - monotonic()
    if (Math.abs(current - offset) <= limits.usageRestoreJumpMs) return false
    offset = current
    relay = `relay.${randomUUID()}`
    forceReport = true
    return true
  }

  const schedule = delay => {
    if (stopped) return
    const at = monotonic() + delay
    if (timer && at >= dueAt) return
    clearTimer(timer)
    dueAt = at
    timer = setTimer(() => { timer = null; dueAt = Infinity; void cycle() }, delay)
  }

  // The relay's internal token: only the gateway grants the hooks access kind.
  const token = () => {
    const claims = { role: config.role, exp: Math.ceil(now() / 1000) + Math.ceil(limits.queryMs / 1000) + 5, gregale_access: 'hooks' }
    if (config.sqlDeadline) claims.gregale_sql_deadline = { version: 1, expires_at_ms: sqlDeadlineExpiry(now() + limits.queryMs), coordinated_refresh: config.coordinatedRefresh === true }
    if (config.coordinatedRefresh) claims.gregale_serving_fingerprint = config.fingerprint
    return new SignJWT(claims).setProtectedHeader({ alg: 'HS256' }).sign(key)
  }

  // loopback calls PostgREST as the relay. It never queues for the engine;
  // settlement, which follows a publication, waits a little for a free slot.
  async function loopback(method, path, body, wait = 0) {
    let release = tryAcquire()
    for (const until = monotonic() + wait; !release && monotonic() < until;) {
      await sleep(50)
      release = tryAcquire()
    }
    if (!release) return { status: 0, busy: true }
    try {
      const payload = body === undefined ? null : Buffer.from(JSON.stringify(body))
      const headers = { authorization: `Bearer ${await token()}`, accept: 'application/json' }
      if (payload) Object.assign(headers, { 'content-type': 'application/json', 'content-profile': settings.schema, 'content-length': String(payload.length) })
      else Object.assign(headers, { 'accept-profile': settings.schema, prefer: 'count=exact' })
      return await new Promise(resolve => {
        const request = http.request({ hostname: '127.0.0.1', port: upstreamPort, path, method, signal: AbortSignal.timeout(limits.queryMs), headers }, response => {
          const chunks = []
          let bytes = 0
          response.on('data', chunk => {
            bytes += chunk.length
            if (bytes > limits.responseBytes) { response.destroy(); resolve({ status: 0 }) } else chunks.push(chunk)
          })
          response.on('error', () => resolve({ status: 0 }))
          response.on('end', () => {
            let json = null
            try { json = JSON.parse(Buffer.concat(chunks).toString('utf8') || 'null') } catch { /* status decides */ }
            resolve({ status: response.statusCode, json, range: response.headers['content-range'] })
          })
        })
        request.on('error', () => resolve({ status: 0 }))
        request.end(payload ?? undefined)
      })
    } finally { release() }
  }

  // rpc calls a relay function.
  const rpc = (name, body, wait = 0) => loopback('POST', `/rpc/${name}`, body, wait)

  // count reads one row of the change event table matching filter, in order,
  // with the number of matching rows; null when it cannot be read.
  async function count(filter, order) {
    const answer = await loopback('GET', `/${changeEventsTable}?${filter}&order=${order}&limit=1`)
    const total = /\/(\d+)$/.exec(answer.range ?? '')
    if ((answer.status !== 200 && answer.status !== 206) || !Array.isArray(answer.json) || !total) return null
    return { rows: answer.json, total: Number(total[1]) }
  }

  // backlog reads delivery from the table: waiting changes and the oldest
  // one, rejected changes and the latest code, and whether the head of the
  // queue waits for a retry (publications keep failing).
  async function backlog() {
    const waiting = await count('select=captured_at,available_at&failed_at=is.null', 'id.asc')
    const failed = waiting && await count('select=failure_code&failed_at=not.is.null', 'failed_at.desc')
    if (!waiting || !failed) return null
    const head = waiting.rows[0]
    const code = failed.rows[0]?.failure_code
    return {
      pending: waiting.total, failed: failed.total,
      oldestPendingAt: waiting.total && typeof head?.captured_at === 'string' ? new Date(head.captured_at).toISOString() : null,
      publishing: Date.parse(head?.available_at) > now() ? 'retrying' : 'ok',
      lastFailureCode: failed.total && failureCode.test(code ?? '') ? code : null,
      lastDroppedAt: await lastDropped(),
    }
  }

  // lastDropped reads when capture last left a change out at the backlog
  // bound (amendment 3); null when it never did, or the installation
  // predates the note (hooks export --upgrade adds it).
  async function lastDropped() {
    const answer = await loopback('GET', `/${changeDropsTable}?select=last_dropped_at`)
    const value = (answer.status === 200 || answer.status === 206) && Array.isArray(answer.json) ? answer.json[0]?.last_dropped_at : null
    const at = typeof value === 'string' ? Date.parse(value) : NaN
    return Number.isNaN(at) ? null : new Date(at).toISOString()
  }

  // workloadToken returns the runtime's platform token, or null.
  async function workloadToken(signal) {
    const identity = await fetchImpl(settings.identity, { signal, redirect: 'error' })
    if (!identity.ok) { await identity.body?.cancel(); return null }
    const body = await identity.json()
    if (body?.token_type !== 'Bearer' || typeof body.access_token !== 'string' || !body.access_token || body.access_token.length > limits.healthBytes) return null
    return body.access_token
  }

  // report sends delivery when it changed (at most once a reporting interval)
  // or when the last report is a heartbeat old. A platform without the route
  // (an older release) is not asked again until the runtime restarts.
  async function report(force = false) {
    const at = monotonic()
    if (!force && (reportsRefused || at - reportedAt < limits.changeHookReportIntervalMs)) return null
    const state = await backlog()
    if (!state || reportsRefused) return state
    const body = { version: 1, pending: state.pending, failed: state.failed, publishing: state.publishing,
      ...(state.oldestPendingAt ? { oldest_pending_at: state.oldestPendingAt } : {}), ...(publishedAt ? { last_published_at: publishedAt } : {}),
      ...(state.lastFailureCode ? { last_failure_code: state.lastFailureCode } : {}), ...(state.lastDroppedAt ? { last_dropped_at: state.lastDroppedAt } : {}) }
    const signature = JSON.stringify(body)
    if (Buffer.byteLength(signature) > limits.changeHookReportBytes) return state
    if (!force && signature === reported && at - reportedAt < limits.changeHookReportHeartbeatMs) return state
    reportedAt = at
    try {
      const signal = AbortSignal.timeout(limits.changeEventsPublishMs)
      const accessToken = await workloadToken(signal)
      if (!accessToken) return state
      const response = await fetchImpl(settings.status, { method: 'POST', redirect: 'error', signal,
        headers: { Authorization: `Bearer ${accessToken}`, 'Content-Type': 'application/json' }, body: signature })
      await response.body?.cancel()
      if (response.status === 404 || response.status === 403) reportsRefused = true
      else if (response.ok) reported = signature
    } catch { /* Never log tokens or platform errors; the next interval retries. */ }
    return state
  }

  // settleDrains answers drain requests: this relay has nothing more to do now.
  function settleDrains() {
    for (const resolve of drains) resolve()
    drains.clear()
  }

  // publish returns per-event results, a whole-publication rejection, or null
  // when the platform could not be reached and everything should wait.
  async function publish(events) {
    try {
      const signal = AbortSignal.timeout(limits.changeEventsPublishMs)
      const accessToken = await workloadToken(signal)
      if (!accessToken) return null
      const response = await fetchImpl(settings.endpoint, { method: 'POST', redirect: 'error', signal,
        headers: { Authorization: `Bearer ${accessToken}`, 'Content-Type': 'application/json' },
        body: JSON.stringify({ version: 1, events }) })
      if (response.ok) {
        const answer = await response.json()
        return Array.isArray(answer?.results) ? { results: answer.results } : null
      }
      await response.body?.cancel()
      // A runtime the platform refuses (403) never publishes; its changes stay.
      if (response.status === 403) return { refused: true }
      // A refused publication cannot succeed on retry; the owner can retry it.
      return [400, 413, 422].includes(response.status) ? { rejected: `publication_rejected_${response.status}` } : null
    } catch { return null /* Never log tokens or platform errors. */ }
  }

  async function cycle() {
    if (stopped) return
    if (busy) { again = true; return }
    busy = true
    let next = idle
    try {
      if (restored()) idle = limits.changeEventsIdleMinMs
      const claim = await rpc('gregale_change_claim', { relay, max_events: limits.changeEventsBatch, lease_ms: limits.changeEventsLeaseMs })
      if (claim.busy) { next = limits.changeEventsIdleMinMs; return }
      if (claim.status !== 200 || !Array.isArray(claim.json)) { next = retry; retry = Math.min(retry * 2, limits.changeEventsIdleMaxMs); settleDrains(); return }
      const rows = claim.json.sort((a, b) => a?.id - b?.id)
      if (!rows.length) {
        next = idle
        idle = Math.min(idle * 2, limits.changeEventsIdleMaxMs)
        await report(forceReport)
        forceReport = false
        settleDrains()
        return
      }
      idle = limits.changeEventsIdleMinMs
      const invalid = rows.filter(row => !validRow(row))
      const batch = rows.filter(validRow)
      const events = pack(batch.map(changeEvent))
      const sent = batch.slice(0, events.length)
      const answer = events.length ? await publish(events) : { results: [] }
      if (answer?.refused) {
        // Return the claim untouched and stop: nothing here will publish.
        await rpc('gregale_change_settle', { relay, published: [], failed: [], retry_after_ms: 0 }, limits.changeEventsPublishMs)
        refused = true
        runtimeFailure('data_api_change_hooks_not_production')
        settleDrains()
        return
      }
      const outcome = answer?.rejected ? { published: [], failed: sent.map(row => ({ id: row.id, code: answer.rejected })), retry: false } : settlement(sent, answer?.results ?? null)
      outcome.failed.push(...invalid.filter(row => Number.isSafeInteger(row?.id)).map(row => ({ id: row.id, code: 'invalid_change_event' })))
      const settled = await rpc('gregale_change_settle', { relay, published: outcome.published, failed: outcome.failed, retry_after_ms: outcome.retry ? retry : 0 }, limits.changeEventsPublishMs)
      if (outcome.failed.length) runtimeFailure('data_api_change_event_rejected')
      if (settled.status !== 200 && settled.status !== 204) runtimeFailure('data_api_change_events_unsettled')
      if (outcome.published.length && (settled.status === 200 || settled.status === 204)) publishedAt = new Date(now()).toISOString()
      if (outcome.retry) { next = retry; retry = Math.min(retry * 2, limits.changeEventsIdleMaxMs) } else { retry = limits.changeEventsIdleMinMs; next = 0 }
      // Publications starting or stopping to fail, and rejections, are news;
      // steady progress and repeated retries wait for the reporting interval.
      const flipped = outcome.retry !== retrying
      retrying = outcome.retry
      await report(forceReport || flipped || outcome.failed.length > 0)
      forceReport = false
      if (outcome.retry) settleDrains()
    } catch { next = retry } finally {
      busy = false
      if (again) { again = false; next = 0 }
      if (!refused) schedule(next)
    }
  }

  // drain publishes what is waiting now, for a platform cron that woke a
  // parked API: it resolves when this relay has nothing more to do or after
  // waitMs, then reports delivery and answers it.
  async function drain(waitMs) {
    if (stopped) return null
    if (refused) return { refused: true }
    await new Promise(resolve => {
      const deadline = setTimeout(() => { drains.delete(done); resolve() }, waitMs)
      deadline.unref?.()
      const done = () => { clearTimeout(deadline); resolve() }
      drains.add(done)
      idle = limits.changeEventsIdleMinMs
      if (busy) again = true
      else schedule(0)
    })
    return report(true)
  }

  schedule(0)
  return Object.freeze({
    kick: () => { if (refused) return; idle = limits.changeEventsIdleMinMs; schedule(0) },
    stop: () => { stopped = true; clearTimer(timer); settleDrains() },
    drain,
    // Tests drive cycles directly.
    cycle,
  })
}
