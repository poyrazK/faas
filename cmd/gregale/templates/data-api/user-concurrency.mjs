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

// admitServerKeyConcurrency bounds the admission slots server-key requests
// hold together (ADR-20261021180534). It refuses at once instead of queueing.
export function admitServerKeyConcurrency(counter, max, res, problem) {
  if (counter.active >= max) {
    const refusal = { reason: 'server_key', max_concurrent: max, active: counter.active, cooling_down: 0, retry_after_seconds: limits.overloadRetrySeconds }
    markConcurrencyLimit(res, refusal)
    res.setHeader('Retry-After', String(refusal.retry_after_seconds))
    res.setHeader('Connection', 'close')
    problem(res, 429, 'data_api_concurrency_limited', { ...refusal, limit: { concurrent_server_key_requests: max }, observed: { active_requests: counter.active },
      docs: 'https://gregale.dev/docs/data-api#server-key-limits' })
    return false
  }
  counter.active++
  let done = false
  const finish = () => {
    if (done) return
    done = true
    counter.active--
  }
  res.once('finish', finish)
  res.once('close', finish)
  return true
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
