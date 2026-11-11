import { readFileSync } from 'node:fs'
import http from 'node:http'
import net from 'node:net'
import { limits, enginePool } from './config.mjs'
import { createPoolObservation } from './pool.mjs'

// The image's runtime version (ADR-20261031095954); owner tooling compares it
// with the runtime the platform currently serves. Local runs may lack it.
export function readRuntimeVersion(file = new URL('./runtime-version.json', import.meta.url)) {
  try {
    const { version } = JSON.parse(readFileSync(file, 'utf8'))
    return typeof version === 'string' && /^[0-9a-f]{16}$/.test(version) ? version : 'unknown'
  } catch {
    return 'unknown'
  }
}

// isReady is /healthz's readiness: engine SQL ready and the refresh reader,
// when there is one, answering.
function isReady(sql, gate) {
  return sql === 'ready' && gate !== 'unavailable'
}

// Share in-flight work and cache completed ready checks briefly. Reuse the existing
// engine pool and reserved refresh reader; never create a diagnostic SQL pool.
export function createHealth(config, upstreamPort, readyPort, { monotonic = () => performance.now() } = {}) {
  const runtimeVersion = config.runtimeVersion ?? readRuntimeVersion()
  const pool = config.poolObservation ?? createPoolObservation(readyPort, enginePool(config))
  const pending = new Map()
  const bounded = (work, fallback) => new Promise(resolve => {
    const timer = setTimeout(() => resolve(fallback), limits.healthProbeMs)
    work.then(value => { clearTimeout(timer); resolve(value) })
  })
  function probe(path) {
    if (pending.has(path)) return bounded(pending.get(path), 'unknown')
    const result = new Promise(resolve => {
      let settled = false
      const finish = value => {
        if (settled) return
        settled = true
        clearTimeout(timer)
        resolve(value)
      }
      const request = http.get({ hostname: '127.0.0.1', port: readyPort, path, agent: false }, response => {
        finish(response.statusCode === 200 ? 'ready' : 'unready')
        response.destroy() // Never retain or publish upstream response bodies.
      })
      // A caller's shorter deadline does not open another engine pool waiter.
      // Retain at most one admin request until it finishes or this hard cap.
      const timer = setTimeout(() => { finish('unknown'); request.destroy() }, limits.queryMs)
      request.on('error', () => finish('unknown'))
    }).finally(() => pending.delete(path))
    pending.set(path, result)
    return bounded(result, 'unknown')
  }
  function engine() {
    if (pending.has('engine')) return pending.get('engine')
    const result = new Promise(resolve => {
      const socket = net.createConnection({ host: '127.0.0.1', port: upstreamPort })
      let settled = false
      const finish = state => {
        if (settled) return
        settled = true
        clearTimeout(timer)
        socket.destroy()
        resolve(state)
      }
      const timer = setTimeout(() => finish('unreachable'), limits.healthProbeMs)
      socket.once('connect', () => finish('reachable'))
      socket.once('error', () => finish('unreachable'))
    }).finally(() => pending.delete('engine'))
    pending.set('engine', result)
    return result
  }
  let reading
  function refresh() {
    if (!config.coordinatedRefresh) return Promise.resolve('disabled')
    if (typeof config.refreshStatus !== 'function') return Promise.resolve('unavailable')
    if (!reading) {
      // Keep the reader shared until it actually settles, even after a probe
      // deadline, so repeated health checks cannot queue more SQL reads.
      reading = Promise.resolve().then(() => config.refreshStatus()).then(gate => {
        if (typeof gate?.paused !== 'boolean') return 'unavailable'
        return gate.paused ? 'paused' : 'active'
      }, () => 'unavailable').finally(() => { reading = undefined })
    }
    return bounded(reading, 'unavailable')
  }
  // /healthz is public and its checks reach the database (PostgREST's /ready
  // and the refresh reader). Concurrent checks share one running set. A
  // completed ready set answers further checks for healthCacheMs, so polling
  // a healthy API asks the database at most once per interval; admission is
  // read live, it costs nothing. An unready set is never reused: the platform
  // polls readiness while an engine starts, and a remembered "not yet" would
  // hold back a ready instance. invalidate() forgets every set when the
  // engine stops, starts, reloads or is restored, so no answer outlives the
  // engine it describes (a restore resumes the monotonic clock where the
  // snapshot paused it).
  // Local development and real-engine tests set config.healthCacheMs to 0.
  const cacheMs = config.healthCacheMs ?? limits.healthCacheMs
  let checked = null
  function checks() {
    if (checked && (checked.pending || (checked.ready && monotonic() - checked.at < cacheMs))) return checked.result
    const entry = { pending: true, ready: false, at: 0 }
    entry.result = Promise.all([probe('/ready'), engine(), refresh(), pool()])
    checked = entry
    // A completed result is cached from when it completed.
    void entry.result.then(([sql, , gate]) => {
      entry.pending = false
      entry.ready = isReady(sql, gate)
      entry.at = monotonic()
    })
    return entry.result
  }
  return {
    sql: () => probe('/ready'),
    // invalidate drops completed and running check sets: neither describes an
    // engine that stopped, started, reloaded or was restored since.
    invalidate() { checked = null },
    async snapshot(admission) {
      // PostgREST 14.3's admin /live also checks SQL with listeners disabled.
      // A TCP check observes only the engine listener, never pool availability.
      const [sql, listener, gate, poolObservation] = await checks()
      return { version: 1, runtime_version: runtimeVersion, live: true, ready: isReady(sql, gate),
        checks: { engine: listener,
          sql, database: sql === 'ready' || gate === 'active' || gate === 'paused' ? 'reachable' : 'unknown', refresh: gate },
        admission: admission(), pool: poolObservation, ...(gate === 'active' || gate === 'paused' ? { refresh_paused: gate === 'paused' } : {}) }
    },
  }
}
