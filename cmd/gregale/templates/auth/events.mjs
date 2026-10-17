// Lifecycle events (ADR-1190). Triggers on GoTrue's tables write an outbox in
// gregale_auth (sql/0003); this module publishes it as app-scoped events
// (ADR-929) with the events:publish key. It drains after requests to this
// app and at startup, and retries failures with backoff, but never polls an
// empty outbox: an idle app neither wakes nor keeps its database awake.
import { limits } from './config.mjs'
import { workflowsConfig } from './workflows.mjs'

// lifecycleConfig is null unless AUTH_LIFECYCLE_EVENTS=1, which needs the
// same events key and API settings as workflow starts.
export function lifecycleConfig(env) {
  const flag = env.AUTH_LIFECYCLE_EVENTS ?? '0'
  if (flag !== '0' && flag !== '1') throw new Error('Invalid AUTH_LIFECYCLE_EVENTS')
  if (flag === '0') return null
  const config = workflowsConfig(env)
  if (!config) throw new Error('AUTH_LIFECYCLE_EVENTS requires AUTH_EVENTS_KEY')
  return config
}

// setLifecycleEvents switches the triggers on or off at startup. Turning
// them off drops undelivered events, which then have no consumer.
export async function setLifecycleEvents(pool, enabled) {
  await pool.query('UPDATE gregale_auth.event_settings SET enabled = $1', [enabled])
  if (!enabled) await pool.query('DELETE FROM gregale_auth.event_outbox')
}

// Claiming leases rows, so instances draining at once do not publish the
// same event twice; a crashed claim becomes due again when its lease ends.
const claimSQL = `WITH due AS (
    SELECT id FROM gregale_auth.event_outbox WHERE next_attempt_at <= now() ORDER BY occurred_at LIMIT $1 FOR UPDATE SKIP LOCKED)
  UPDATE gregale_auth.event_outbox o SET attempts = o.attempts + 1, next_attempt_at = now() + make_interval(secs => $2)
  FROM due WHERE o.id = due.id
  RETURNING o.id, o.type, o.user_id, o.data, o.occurred_at, o.attempts`

export function backoffSeconds(attempts) {
  return Math.min(limits.eventMaxBackoffSeconds, 5 * 2 ** Math.min(attempts, 20))
}

// publishEvent returns 'delivered', 'retry' or 'rejected'. The key is the
// outbox row's ID, so a retried publish is deduplicated by Gregale.
export async function publishEvent(config, row, fetchImpl = fetch) {
  const body = {
    key: `auth-event.${row.id}`, type: row.type, time: new Date(row.occurred_at).toISOString(),
    data: { user_id: row.user_id, ...row.data },
  }
  let response
  try {
    response = await fetchImpl(`${config.api}/v1/apps/${config.slug}/events:publish`, {
      method: 'POST', redirect: 'error', signal: AbortSignal.timeout(10000),
      headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${config.key}` },
      body: JSON.stringify(body),
    })
    await response.arrayBuffer()
  } catch {
    return 'retry'
  }
  // 409 means this key was already accepted; the event is not lost.
  if (response.ok || response.status === 409) return 'delivered'
  if (response.status === 408 || response.status === 429 || response.status >= 500) return 'retry'
  return 'rejected'
}

export function createEventPublisher({ pool, config, fetchImpl = fetch, log = console.error }) {
  let running = null, again = false, retry = null
  async function drainOnce() {
    const expired = await pool.query('DELETE FROM gregale_auth.event_outbox WHERE occurred_at < now() - make_interval(secs => $1)', [limits.eventRetentionSeconds])
    if (expired.rowCount) log(JSON.stringify({ level: 'warning', event: 'lifecycle_events_expired', count: expired.rowCount }))
    const { rows } = await pool.query(claimSQL, [limits.eventBatch, limits.eventLeaseSeconds])
    const delivered = [], rejected = []
    let next = 0
    const worker = async () => {
      while (next < rows.length) {
        const row = rows[next++]
        const outcome = await publishEvent(config, row, fetchImpl)
        if (outcome === 'delivered') delivered.push(row.id)
        else if (outcome === 'rejected') rejected.push(row)
        else await pool.query('UPDATE gregale_auth.event_outbox SET next_attempt_at = now() + make_interval(secs => $2) WHERE id = $1', [row.id, backoffSeconds(row.attempts)])
      }
    }
    await Promise.all(Array.from({ length: Math.min(4, rows.length) }, worker))
    if (delivered.length || rejected.length) await pool.query('DELETE FROM gregale_auth.event_outbox WHERE id = ANY($1::uuid[])', [[...delivered, ...rejected.map(row => row.id)]])
    for (const row of rejected) log(JSON.stringify({ level: 'error', event: 'lifecycle_event_rejected', type: row.type }))
    return rows.length === limits.eventBatch
  }
  // scheduleRetry wakes the publisher for the earliest failed event while
  // this instance runs; a parked instance resumes it on its next request.
  async function scheduleRetry() {
    const { rows: [due] } = await pool.query('SELECT extract(epoch FROM min(next_attempt_at) - now()) AS seconds FROM gregale_auth.event_outbox')
    if (retry) clearTimeout(retry)
    retry = null
    if (due?.seconds === null || due?.seconds === undefined) return
    retry = setTimeout(poke, Math.max(1, Number(due.seconds)) * 1000)
    retry.unref?.()
  }
  async function drain() {
    try {
      for (let round = 0; round < 20 && await drainOnce(); round++);
      await scheduleRetry()
    } catch (err) {
      log(JSON.stringify({ level: 'error', event: 'lifecycle_events_failed', sqlstate: err?.code ?? null }))
    }
  }
  function poke() {
    if (running) { again = true; return running }
    running = drain().finally(() => {
      running = null
      if (again) { again = false; poke() }
    })
    return running
  }
  return { poke, drain }
}

// publishesAfter reports whether a request may have changed users or
// sessions, so the publisher should drain after it. Health checks, the
// JWKS, settings and static pages never do.
export function publishesAfter(req) {
  const path = (req.url ?? '').split('?')[0]
  if (path.startsWith('/admin/v1/') || path.startsWith('/scim/v2/')) return req.method !== 'GET'
  if (!path.startsWith('/auth/v1/')) return false
  return !(req.method === 'GET' && ['/auth/v1/.well-known/jwks.json', '/auth/v1/settings', '/auth/v1/health', '/auth/v1/user'].includes(path))
}
