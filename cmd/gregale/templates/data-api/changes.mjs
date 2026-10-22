import http from 'node:http'
import { randomUUID } from 'node:crypto'
import pg from 'pg'
import { SignJWT } from 'jose'
import { limits } from './config.mjs'
import { runtimeFailure } from './logging.mjs'

// Change hooks (ADR-20261022121642). Owner triggers record each change of a
// hooked table in <schema>.gregale_change_events inside the writing
// transaction. The relay claims recorded changes in order through PostgREST,
// publishes them as app-scoped Gregale events under the runtime's workload
// identity and settles them: published events are deleted and rejected ones
// marked failed. One relay per database publishes at a time.

export const changeEventsTable = 'gregale_change_events'

const operations = { insert: 'inserted', update: 'updated', delete: 'deleted' }
const eventKey = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/
const failureCode = /^[a-z0-9_]{1,64}$/

const changeEventsQuery = `SELECT c.relkind AS kind, c.relrowsecurity AS rls,
 has_table_privilege(current_user, c.oid, 'SELECT') AND has_table_privilege(current_user, c.oid, 'UPDATE') AND has_table_privilege(current_user, c.oid, 'DELETE') AS settles,
 (SELECT count(*) FROM pg_proc p WHERE p.pronamespace = c.relnamespace AND p.proname IN ('gregale_change_claim', 'gregale_change_settle')
   AND NOT p.prosecdef AND has_function_privilege(current_user, p.oid, 'EXECUTE'))::int AS functions
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

export async function checkChangeEvents(connection, schema) {
  const client = new pg.Client({ connectionString: connection, statement_timeout: limits.queryMs, connectionTimeoutMillis: limits.healthProbeMs })
  await client.connect()
  try {
    const { rows } = await client.query(changeEventsQuery, [schema, changeEventsTable])
    const unsafe = unsafeChangeEvents(rows[0])
    if (unsafe) throw new Error(`Change hooks are not installed: ${unsafe}`)
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
  return { id: row.event_key, type: `${row.relation_schema}.${row.relation_name}.${operations[row.op]}`, time: row.captured_at, data }
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

  // Snapshot restores copy this process; each copy takes a new identity so
  // copies never share a claim.
  const restored = () => {
    const current = now() - monotonic()
    if (Math.abs(current - offset) <= limits.usageRestoreJumpMs) return false
    offset = current
    relay = `relay.${randomUUID()}`
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
    if (config.sqlDeadline) claims.gregale_sql_deadline = { version: 1, expires_at_ms: now() + limits.queryMs, coordinated_refresh: config.coordinatedRefresh === true }
    if (config.coordinatedRefresh) claims.gregale_serving_fingerprint = config.fingerprint
    return new SignJWT(claims).setProtectedHeader({ alg: 'HS256' }).sign(key)
  }

  // rpc calls a relay function. It never queues for the engine; settlement,
  // which follows a publication, waits a little for a free slot.
  async function rpc(name, body, wait = 0) {
    let release = tryAcquire()
    for (const until = monotonic() + wait; !release && monotonic() < until;) {
      await sleep(50)
      release = tryAcquire()
    }
    if (!release) return { status: 0, busy: true }
    try {
      const payload = Buffer.from(JSON.stringify(body))
      const authorization = `Bearer ${await token()}`
      return await new Promise(resolve => {
        const request = http.request({ hostname: '127.0.0.1', port: upstreamPort, path: `/rpc/${name}`, method: 'POST', signal: AbortSignal.timeout(limits.queryMs),
          headers: { authorization, accept: 'application/json', 'content-type': 'application/json', 'content-profile': settings.schema, 'content-length': String(payload.length) } }, response => {
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
            resolve({ status: response.statusCode, json })
          })
        })
        request.on('error', () => resolve({ status: 0 }))
        request.end(payload)
      })
    } finally { release() }
  }

  // publish returns per-event results, a whole-publication rejection, or null
  // when the platform could not be reached and everything should wait.
  async function publish(events) {
    try {
      const signal = AbortSignal.timeout(limits.changeEventsPublishMs)
      const identity = await fetchImpl(settings.identity, { signal, redirect: 'error' })
      if (!identity.ok) { await identity.body?.cancel(); return null }
      const body = await identity.json()
      if (body?.token_type !== 'Bearer' || typeof body.access_token !== 'string' || !body.access_token || body.access_token.length > limits.healthBytes) return null
      const response = await fetchImpl(settings.endpoint, { method: 'POST', redirect: 'error', signal,
        headers: { Authorization: `Bearer ${body.access_token}`, 'Content-Type': 'application/json' },
        body: JSON.stringify({ version: 1, events }) })
      if (response.ok) {
        const answer = await response.json()
        return Array.isArray(answer?.results) ? { results: answer.results } : null
      }
      await response.body?.cancel()
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
      if (claim.status !== 200 || !Array.isArray(claim.json)) { next = retry; retry = Math.min(retry * 2, limits.changeEventsIdleMaxMs); return }
      const rows = claim.json.sort((a, b) => a?.id - b?.id)
      if (!rows.length) { next = idle; idle = Math.min(idle * 2, limits.changeEventsIdleMaxMs); return }
      idle = limits.changeEventsIdleMinMs
      const invalid = rows.filter(row => !validRow(row))
      const batch = rows.filter(validRow)
      const events = pack(batch.map(changeEvent))
      const sent = batch.slice(0, events.length)
      const answer = events.length ? await publish(events) : { results: [] }
      const outcome = answer?.rejected ? { published: [], failed: sent.map(row => ({ id: row.id, code: answer.rejected })), retry: false } : settlement(sent, answer?.results ?? null)
      outcome.failed.push(...invalid.filter(row => Number.isSafeInteger(row?.id)).map(row => ({ id: row.id, code: 'invalid_change_event' })))
      const settled = await rpc('gregale_change_settle', { relay, published: outcome.published, failed: outcome.failed, retry_after_ms: outcome.retry ? retry : 0 }, limits.changeEventsPublishMs)
      if (outcome.failed.length) runtimeFailure('data_api_change_event_rejected')
      if (settled.status !== 200 && settled.status !== 204) runtimeFailure('data_api_change_events_unsettled')
      if (outcome.retry) { next = retry; retry = Math.min(retry * 2, limits.changeEventsIdleMaxMs) } else { retry = limits.changeEventsIdleMinMs; next = 0 }
    } catch { next = retry } finally {
      busy = false
      if (again) { again = false; next = 0 }
      schedule(next)
    }
  }

  schedule(0)
  return Object.freeze({
    kick: () => { idle = limits.changeEventsIdleMinMs; schedule(0) },
    stop: () => { stopped = true; clearTimer(timer) },
    // Tests drive cycles directly.
    cycle,
  })
}
