import { limits } from './config.mjs'
import { markResponseLimit } from './logging.mjs'

// Count actual engine body bytes without buffering the response. Pause the
// engine when the downstream write buffer fills, and resume only after drain.
export function streamResponse(response, res, method, deadline, problem, publicCache = null) {
  let bytes = 0, stopped = false
  const out = { 'Cache-Control': 'no-store' }
  // Only a complete success may become a shared edge entry.
  if (publicCache && response.statusCode === 200) Object.assign(out, { 'Cache-Control': publicCache.cacheControl, 'Cache-Tag': publicCache.tags })
  for (const name of ['content-type', 'content-range', 'preference-applied', 'location']) if (response.headers[name]) out[name] = response.headers[name]
  const headers = () => { if (!res.headersSent) res.writeHead(response.statusCode, out) }
  const stop = () => { stopped = true; res.removeListener('drain', drain) }
  const drain = () => { if (!stopped && deadline.check()) response.resume() }
  const unavailable = () => {
    if (stopped) return
    stop()
    if (deadline.check()) problem(res, 503, 'data_api_unavailable')
    response.destroy()
  }
  const overflow = source => {
    stop()
    markResponseLimit(res, { source, limit_bytes: limits.responseBytes, observed_at_least_bytes: limits.responseBytes + 1 })
    if (!res.headersSent) res.setHeader('Connection', 'close')
    problem(res, 502, 'response_too_large', {
      limit: limits.responseBytes, observed: limits.responseBytes + 1, observed_is_lower_bound: true,
      docs: 'https://gregale.dev/docs/data-api',
    })
    response.destroy()
  }
  response.on('error', unavailable)
  response.on('aborted', unavailable)
  res.once('close', () => { stop(); response.destroy() })
  const bodyless = method === 'HEAD' || response.statusCode === 204 || response.statusCode === 304
  const declared = response.headers['content-length']
  if (!bodyless && typeof declared === 'string' && /^\d+$/.test(declared) && Number(declared) > limits.responseBytes) return overflow('content_length')
  res.on('drain', drain)
  response.on('data', chunk => {
    if (stopped || !deadline.check()) return
    if (chunk.length > limits.responseBytes - bytes) return overflow('stream')
    bytes += chunk.length
    headers()
    if (!res.write(chunk)) response.pause()
  })
  response.on('end', () => {
    if (stopped || !deadline.check()) return
    stop(); headers(); res.end()
  })
}
