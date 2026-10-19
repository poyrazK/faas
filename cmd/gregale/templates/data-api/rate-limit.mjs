import { createHash } from 'node:crypto'
import { limits, rateLimitPolicy } from './config.mjs'
import { markRateLimit } from './logging.mjs'

// One limiter per server. Only verified subjects reach take(); tokens and raw
// subjects are never retained. Fully refilled buckets may be discarded safely.
export function createRateLimiter(value, clock = () => performance.now()) {
  const policy = rateLimitPolicy(value), buckets = new Map()
  let sweepAt = 0
  const tokensAt = (bucket, now) => Math.min(policy.burst, bucket.tokens + Math.max(0, now - bucket.at) * policy.perSecond / 1000)
  return {
    get tracked() { return buckets.size },
    take(subject) {
      if (!policy) return null
      const now = clock()
      if (now >= sweepAt) {
        for (const [key, bucket] of buckets) if (tokensAt(bucket, now) >= policy.burst) buckets.delete(key)
        sweepAt = now + limits.rateLimitSweepMs
      }
      const key = createHash('sha256').update(JSON.stringify(subject)).digest('hex')
      let bucket = buckets.get(key), reason
      if (!bucket && buckets.size >= limits.rateLimitMaxSubjects) reason = 'tracking_capacity'
      else {
        if (!bucket) { bucket = { tokens: policy.burst, at: now }; buckets.set(key, bucket) }
        bucket.tokens = tokensAt(bucket, now); bucket.at = now
        if (bucket.tokens >= 1) { bucket.tokens--; return null }
        reason = 'subject'
      }
      return { reason, per_second: policy.perSecond, burst: policy.burst,
        retry_after_seconds: reason === 'subject' ? Math.max(1, Math.ceil((1 - bucket.tokens) / policy.perSecond)) : Math.max(1, Math.ceil((sweepAt - now) / 1000)) }
    },
  }
}

export function admitRateLimit(limiter, subject, res, problem) {
  const rejected = limiter.take(subject)
  if (!rejected) return true
  markRateLimit(res, rejected)
  res.setHeader('Retry-After', String(rejected.retry_after_seconds))
  res.setHeader('Connection', 'close')
  problem(res, 429, 'data_api_rate_limited', { ...rejected,
    limit: rejected.reason === 'subject' ? { per_second: rejected.per_second, burst: rejected.burst } : { tracked_subjects: limits.rateLimitMaxSubjects },
    observed: rejected.reason === 'subject' ? { available_requests: 0 } : { tracked_subjects: limits.rateLimitMaxSubjects },
    docs: 'https://gregale.dev/docs/data-api' })
  return false
}
