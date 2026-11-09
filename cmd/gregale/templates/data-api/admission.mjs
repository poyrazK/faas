import { limits } from './config.mjs'
import { markAdmission } from './logging.mjs'

// One gate per HTTP server, before asynchronous verification or upstream work.
// A disconnect cannot release an active verifier until that verifier settles.
export function createAdmission(concurrency, reject) {
  if (!Number.isInteger(concurrency) || concurrency < 1 || concurrency > limits.poolMax) throw Error('Invalid admission concurrency')
  let active = 0
  const queue = []
  const gone = (req, res) => req.aborted || res.destroyed || res.writableEnded

  function pump() {
    while (active < concurrency && queue.length) queue[0].admit()
  }

  function acquire(req, res) {
    const started = performance.now()
    const record = outcome => markAdmission(res, {
      outcome, wait_ms: Math.round((performance.now() - started) * 1000) / 1000,
      active, queued: queue.length,
    })
    if (gone(req, res)) { record('cancelled'); return Promise.resolve(false) }
    if (active < concurrency && !queue.length) {
      active++; record('admitted'); return Promise.resolve(true)
    }
    if (queue.length >= limits.queuedRequests) {
      record('queue_full'); reject(res); return Promise.resolve(false)
    }
    return new Promise(resolve => {
      let settled = false
      const finish = outcome => {
        if (settled) return
        settled = true
        clearTimeout(timer)
        req.removeListener('aborted', cancel)
        res.removeListener('close', cancel)
        queue.splice(queue.indexOf(entry), 1)
        if (outcome === 'admitted') active++
        record(outcome)
        if (outcome === 'queue_timeout') reject(res)
        resolve(outcome === 'admitted')
      }
      const cancel = () => { finish('cancelled'); pump() }
      const entry = { admit: () => finish(gone(req, res) ? 'cancelled' : performance.now() - started >= limits.admissionWaitMs ? 'queue_timeout' : 'admitted') }
      const expire = () => {
        const remaining = limits.admissionWaitMs - (performance.now() - started)
        if (remaining > 0) { timer = setTimeout(expire, Math.ceil(remaining)); timer.unref(); return }
        finish('queue_timeout'); pump()
      }
      let timer = setTimeout(expire, limits.admissionWaitMs)
      timer.unref()
      req.once('aborted', cancel)
      // Update cancellation metadata before the request logger observes close.
      res.prependOnceListener('close', cancel)
      queue.push(entry)
      record('queued')
    })
  }

  // handle receives release(), which frees the slot early for work that no
  // longer needs the engine, such as a storage transfer.
  async function admitted(req, res, handle) {
    if (!await acquire(req, res)) return
    let released = false
    const release = () => {
      if (released) return
      released = true
      active--
      pump()
    }
    const ended = new Promise(resolve => {
      if (gone(req, res)) return resolve()
      const finish = () => {
        res.removeListener('finish', finish)
        res.removeListener('close', finish)
        resolve()
      }
      res.once('finish', finish)
      res.once('close', finish)
    })
    try {
      if (!gone(req, res)) await handle(release)
    } finally {
      // Proxy dispatch returns before its response streams. Keep the slot until
      // both asynchronous handler work and the HTTP response have completed.
      await ended
      release()
    }
  }
  // tryAcquire lends a free slot to background work, such as the change
  // event relay, without queuing ahead of requests. It returns release() or
  // null when every slot is busy or a request is waiting.
  function tryAcquire() {
    if (active >= concurrency || queue.length) return null
    active++
    let released = false
    return () => {
      if (released) return
      released = true
      active--
      pump()
    }
  }
  return Object.assign(admitted, { tryAcquire, snapshot: () => ({ active, queued: queue.length, capacity: concurrency, queue_capacity: limits.queuedRequests }) })
}
