import { limits } from './config.mjs'
import { createPoolObservation } from './pool.mjs'

export function qualifiedConnectionEngine(version) {
  return /^PostgREST 14\.3(?:\s|$)/.test(version)
}

// Identity is fetched on every iteration, so snapshot restores acquire the
// current host-stamped instance identity. Never persist tokens in a snapshot.
export function createConnectionReporter(config, pool, fetchImpl = fetch, lifecycle) {
  if (!config.connectionObservation) return async () => {}
  const observation = config.connectionObservation
  return async () => {
    const observedAt = new Date().toISOString()
    const deadline = AbortSignal.timeout(limits.connectionReportMs)
    const signal = lifecycle ? AbortSignal.any([deadline, lifecycle]) : deadline
    try {
      const counts = await pool()
      signal.throwIfAborted()
      const response = await fetchImpl(observation.identity, { signal, redirect: 'error' })
      if (!response.ok) { await response.body?.cancel(); return }
      const reader = response.body.getReader()
      const chunks = []; let bytes = 0
      try {
        for (;;) {
          const { done, value } = await reader.read()
          if (done) break
          bytes += value.length
          if (bytes > limits.healthBytes) { await reader.cancel(); return }
          chunks.push(Buffer.from(value))
        }
      } finally { reader.releaseLock() }
      const identity = JSON.parse(Buffer.concat(chunks).toString('utf8'))
      if (identity.token_type !== 'Bearer' || typeof identity.access_token !== 'string' || !identity.access_token || identity.access_token.length > limits.healthBytes) return
      const engine = config.coordinatedRefresh ? limits.pool - 1 : limits.pool
      const report = { version: 1, binding_id: observation.binding_id,
        database_identity: observation.database_identity, database_generation: observation.database_generation, credential_generation: observation.credential_generation,
        role: config.role, runtime_model: 'postgrest-14.3-pools-v1',
        state: counts?.state === 'observed' && counts.capacity === engine ? 'observed' : 'unknown',
        engine_connections: engine, refresh_connections: config.coordinatedRefresh ? 1 : 0, observed_at: observedAt }
      const result = await fetchImpl(observation.endpoint, { method: 'POST', redirect: 'error', signal,
        headers: { Authorization: `Bearer ${identity.access_token}`, 'Content-Type': 'application/json' }, body: JSON.stringify(report) })
      await result.body?.cancel()
    } catch { /* Missing evidence expires; never log tokens, role or backend errors. */ }
  }
}

export function startConnectionReporter(config, readyPort = 3001) {
  if (!config.connectionObservation) return () => {}
  const pool = config.poolObservation ?? createPoolObservation(readyPort, config.coordinatedRefresh ? limits.pool - 1 : limits.pool)
  const controller = new AbortController()
  const report = createConnectionReporter(config, pool, fetch, controller.signal)
  let stopped = false, timer
  const run = async () => {
    await report()
    if (!stopped) { timer = setTimeout(run, limits.connectionReportIntervalMs); timer.unref() }
  }
  void run()
  return () => { stopped = true; clearTimeout(timer); controller.abort() }
}
