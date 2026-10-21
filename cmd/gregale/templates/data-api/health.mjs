import http from 'node:http'
import net from 'node:net'
import { limits } from './config.mjs'
import { createPoolObservation } from './pool.mjs'

// Share in-flight work without caching completed results. Reuse the existing
// engine pool and reserved refresh reader; never create a diagnostic SQL pool.
export function createHealth(config, upstreamPort, readyPort) {
  const pool = config.poolObservation ?? createPoolObservation(readyPort, config.coordinatedRefresh ? limits.pool - 1 : limits.pool)
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
  return {
    sql: () => probe('/ready'),
    async snapshot(admission) {
      // PostgREST 14.3's admin /live also checks SQL with listeners disabled.
      // A TCP check observes only the engine listener, never pool availability.
      const [sql, listener, gate, poolObservation] = await Promise.all([probe('/ready'), engine(), refresh(), pool()])
      return { version: 1, live: true, ready: sql === 'ready' && gate !== 'unavailable',
        checks: { engine: listener,
          sql, database: sql === 'ready' || gate === 'active' || gate === 'paused' ? 'reachable' : 'unknown', refresh: gate },
        admission: admission(), pool: poolObservation, ...(gate === 'active' || gate === 'paused' ? { refresh_paused: gate === 'paused' } : {}) }
    },
  }
}
