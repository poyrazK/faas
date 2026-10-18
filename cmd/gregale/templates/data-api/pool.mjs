import http from 'node:http'
import { limits } from './config.mjs'

const metrics = Object.freeze({
  pgrst_db_pool_max: 'capacity',
  pgrst_db_pool_available: 'available',
  pgrst_db_pool_waiting: 'waiting',
  pgrst_db_pool_timeouts_total: 'timeouts_total',
})

// The pinned engine exports these four unlabelled integer metrics. In particular,
// available counts existing idle connections, not unused capacity in a lazy pool.
export function parsePoolMetrics(body, capacity) {
  const counts = {}
  for (const line of body.split('\n').map(line => line.trim())) {
    if (!line || line.startsWith('#')) continue
    const name = line.split(/[\s{]/, 1)[0]
    if (!Object.hasOwn(metrics, name)) continue
    const sample = /^\S+[ \t]+([+]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?)$/.exec(line)
    if (!sample || line.includes('{') || Object.hasOwn(counts, metrics[name])) return null
    const value = Number(sample[1])
    if (!Number.isSafeInteger(value) || value < 0) return null
    counts[metrics[name]] = value
  }
  if (Object.keys(counts).length !== Object.keys(metrics).length || counts.capacity !== capacity || counts.available > capacity) return null
  return counts
}

const unknown = reason => ({ state: 'unknown', checked_at: new Date().toISOString(), reason })

// The pinned admin handler checks SQL twice before exporting metrics when its
// listener is disabled. Keep pending work shared after a caller's shorter timeout.
export function createPoolObservation(port, capacity) {
  let pending
  return () => {
    pending ??= new Promise(resolve => {
      let settled = false, response, bytes = 0
      const chunks = []
      const finish = (counts, reason) => {
        if (settled) return
        settled = true
        clearTimeout(timer)
        response?.destroy()
        request.destroy()
        resolve(counts ? { state: 'observed', checked_at: new Date().toISOString(), ...counts } : unknown(reason))
      }
      const request = http.get({ hostname: '127.0.0.1', port, path: '/metrics', agent: false, headers: { Accept: 'text/plain' } }, res => {
        response = res
        res.on('error', () => finish(null, 'unavailable'))
        res.on('aborted', () => finish(null, 'unavailable'))
        if (res.statusCode !== 200) return finish(null, res.statusCode === 404 ? 'unsupported' : 'unavailable')
        res.on('data', chunk => {
          bytes += chunk.length
          if (bytes > limits.healthBytes) return finish(null, 'too_large')
          chunks.push(chunk)
        })
        res.on('end', () => {
          const counts = parsePoolMetrics(Buffer.concat(chunks).toString('utf8'), capacity)
          finish(counts, 'invalid_metrics')
        })
      })
      const timer = setTimeout(() => finish(null, 'timeout'), limits.queryMs)
      request.on('error', () => finish(null, 'unavailable'))
    }).finally(() => { pending = undefined })
    const work = pending
    return new Promise(resolve => {
      const timer = setTimeout(() => resolve(unknown('timeout')), limits.healthProbeMs)
      work.then(value => { clearTimeout(timer); resolve(value) })
    })
  }
}
