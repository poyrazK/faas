import { createHash } from 'node:crypto'
import { limits, userConcurrencyPolicy } from './config.mjs'
import { markConcurrencyLimit } from './logging.mjs'

// HTTP admission protection within one runtime, not a SQL-session fence. Only
// verified subjects reach take(); the bounded map retains digests, never JWTs or
// raw subjects. Pending handlers cannot be evicted. Uncertain dispatches cool
// down for one query budget after both handler and downstream have settled.
export function createUserConcurrency(value, capacity = limits.pool, clock = () => performance.now()) {
  const policy = userConcurrencyPolicy(value, capacity), users = new Map()
  let sweepAt = 0
  const remove = (key, held, entry) => {
    entry.closed = true
    held.delete(entry)
    if (!held.size && users.get(key) === held) users.delete(key)
  }
  const prune = (key, held, now) => {
    for (const entry of held) if (!entry.active && entry.until <= now) remove(key, held, entry)
  }
  return {
    get tracked() { return users.size },
    take(subject) {
      if (policy === null) return { lease: null }
      const now = clock()
      if (now >= sweepAt) {
        for (const [key, held] of users) prune(key, held, now)
        sweepAt = now + limits.rateLimitSweepMs
      }
      const key = createHash('sha256').update(JSON.stringify(subject)).digest('hex')
      let held = users.get(key)
      if (held) { prune(key, held, now); held = users.get(key) }
      let active = 0, cooling = 0, earliest = Infinity
      for (const entry of held ?? []) {
        if (entry.active) active++
        else { cooling++; earliest = Math.min(earliest, entry.until) }
      }
      const reason = held?.size >= policy ? 'subject' : !held && users.size >= limits.userConcurrencyMaxSubjects ? 'tracking_capacity' : null
      if (reason) return { rejected: { reason, max_concurrent: policy, active, cooling_down: cooling,
        retry_after_seconds: active || !cooling ? limits.overloadRetrySeconds : Math.min(Math.ceil(limits.userConcurrencyCooldownMs / 1000), Math.max(limits.overloadRetrySeconds, Math.ceil((earliest - now) / 1000))) } }
      if (!held) { held = new Set(); users.set(key, held) }
      const entry = { active: true, until: 0, closed: false }
      held.add(entry)
      let handlerDone = false, downstreamDone = false, uncertain = false
      const finish = () => {
        if (entry.closed || !handlerDone || !downstreamDone) return
        if (!uncertain) remove(key, held, entry)
        else if (entry.active) { entry.active = false; entry.until = clock() + limits.userConcurrencyCooldownMs }
      }
      return { lease: {
        dispatched() { if (!entry.closed && !handlerDone) uncertain = true },
        completed() { uncertain = false; finish() },
        settled() { handlerDone = true; finish() },
        downstreamDone() { downstreamDone = true; finish() },
      } }
    },
  }
}

// createServerKeyConcurrency bounds the admission slots server-key requests
// hold together, queued or running (ADR-20261021180534), so users and visitors
// keep the rest. A request past the limit waits in FIFO order within
// admission's wait and queue bounds, then is refused. Leases settle like user
// leases: an uncertain dispatch keeps its slot for one query budget, because a
// disconnect does not stop its SQL.
export function createServerKeyConcurrency(max, clock = () => performance.now()) {
  const held = new Set(), waiters = []
  const prune = now => {
    for (const entry of held) if (!entry.active && entry.until <= now) held.delete(entry)
  }
  function pump() {
    prune(clock())
    while (held.size < max && waiters.length) waiters[0].grant()
  }
  const lease = () => {
    const entry = { active: true, until: 0 }
    held.add(entry)
    let handlerDone = false, downstreamDone = false, uncertain = false, closed = false
    const finish = () => {
      if (closed || !handlerDone || !downstreamDone) return
      closed = true
      if (!uncertain) { held.delete(entry); pump(); return }
      entry.active = false
      entry.until = clock() + limits.userConcurrencyCooldownMs
      setTimeout(pump, limits.userConcurrencyCooldownMs).unref()
    }
    return {
      dispatched() { if (!closed && !handlerDone) uncertain = true },
      completed() { uncertain = false; finish() },
      settled() { handlerDone = true; finish() },
      downstreamDone() { downstreamDone = true; finish() },
    }
  }
  const refusal = () => {
    const now = clock()
    let active = 0, cooling = 0, earliest = Infinity
    for (const entry of held) {
      if (entry.active) active++
      else { cooling++; earliest = Math.min(earliest, entry.until) }
    }
    return { reason: 'server_key', max_concurrent: max, active, cooling_down: cooling,
      retry_after_seconds: active || !cooling ? limits.overloadRetrySeconds : Math.min(Math.ceil(limits.userConcurrencyCooldownMs / 1000), Math.max(limits.overloadRetrySeconds, Math.ceil((earliest - now) / 1000))) }
  }
  return {
    // take resolves { lease }, { rejected } past the wait or queue bound, or
    // {} when the client leaves while waiting.
    take(req, res) {
      const gone = () => req.aborted || res.destroyed || res.writableEnded
      pump()
      if (gone()) return Promise.resolve({})
      if (held.size < max) return Promise.resolve({ lease: lease() })
      if (waiters.length >= limits.queuedRequests) return Promise.resolve({ rejected: refusal() })
      return new Promise(resolve => {
        const done = result => {
          clearTimeout(timer)
          req.removeListener('aborted', cancel)
          res.removeListener('close', cancel)
          waiters.splice(waiters.indexOf(waiter), 1)
          resolve(result)
        }
        const cancel = () => { done({}); pump() }
        const waiter = { grant: () => done(gone() ? {} : { lease: lease() }) }
        const timer = setTimeout(() => {
          pump()
          if (waiters.includes(waiter)) done({ rejected: refusal() })
        }, limits.admissionWaitMs)
        timer.unref()
        req.once('aborted', cancel)
        res.once('close', cancel)
        waiters.push(waiter)
      })
    },
  }
}

// admitServerKeyConcurrency resolves a server-key lease, or false once it has
// refused the request or the client has left.
export async function admitServerKeyConcurrency(limiter, req, res, problem) {
  const { lease, rejected } = await limiter.take(req, res)
  if (rejected) {
    markConcurrencyLimit(res, rejected)
    res.setHeader('Retry-After', String(rejected.retry_after_seconds))
    res.setHeader('Connection', 'close')
    problem(res, 429, 'data_api_concurrency_limited', { ...rejected, limit: { concurrent_server_key_requests: rejected.max_concurrent },
      observed: { active_requests: rejected.active, cooling_down_requests: rejected.cooling_down }, docs: 'https://gregale.dev/docs/data-api#server-key-limits' })
    return false
  }
  if (!lease) return false
  const finish = () => {
    res.removeListener('finish', finish)
    res.removeListener('close', finish)
    lease.downstreamDone()
  }
  if (req.aborted || res.destroyed || res.writableEnded) finish()
  else { res.once('finish', finish); res.once('close', finish) }
  return lease
}

export function admitUserConcurrency(limiter, subject, req, res, problem) {
  const { lease, rejected } = limiter.take(subject)
  if (rejected) {
    markConcurrencyLimit(res, rejected)
    res.setHeader('Retry-After', String(rejected.retry_after_seconds))
    res.setHeader('Connection', 'close')
    problem(res, 429, 'data_api_concurrency_limited', { ...rejected,
      limit: rejected.reason === 'subject' ? { concurrent_requests_per_user: rejected.max_concurrent } : { tracked_users: limits.userConcurrencyMaxSubjects },
      observed: rejected.reason === 'subject' ? { active_requests: rejected.active, cooling_down_requests: rejected.cooling_down } : { tracked_users: limits.userConcurrencyMaxSubjects },
      docs: 'https://gregale.dev/docs/data-api' })
    return false
  }
  if (lease) {
    const finish = () => {
      res.removeListener('finish', finish)
      res.removeListener('close', finish)
      lease.downstreamDone()
    }
    if (req.aborted || res.destroyed || res.writableEnded) finish()
    else { res.once('finish', finish); res.once('close', finish) }
  }
  return lease
}
