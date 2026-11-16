// Run as an optional scheduled Gregale Job using this runtime image. Each
// invocation wakes the API, which publishes with its own app identity.
import { pathToFileURL } from 'node:url'
export async function pumpHooks({ url, key, fetchImpl = fetch, signal = AbortSignal.timeout(110000), maxBatches = 10, log = value => console.log(JSON.stringify(value)) }) {
  const target = new URL(url)
  if ((target.protocol !== 'https:' && !(target.protocol === 'http:' && ['127.0.0.1','localhost','[::1]'].includes(target.hostname))) || target.username || target.password || target.search || target.hash || target.pathname !== '/' || !/^gdsk_[A-Za-z0-9_-]{43}$/.test(key ?? '') || !Number.isInteger(maxBatches) || maxBatches < 1 || maxBatches > 10) throw new Error('Invalid hooks worker configuration')
  let previous = Infinity, status
  for (let batch = 0; batch < maxBatches; batch++) {
    signal.throwIfAborted()
    const response = await fetchImpl(new URL('/__gregale/hooks/drain', target), { method: 'POST', headers: { authorization: `Bearer ${key}` }, redirect: 'error', credentials: 'omit', signal: AbortSignal.any([signal, AbortSignal.timeout(45000)]) })
    if (!response.ok) { await response.body?.cancel(); throw new Error(`Hooks pump refused (${response.status})`) }
    const reader = response.body.getReader(); let bytes = 0, text = ''
    for (;;) { const chunk = await reader.read(); if (chunk.done) break; bytes += chunk.value.length; if (bytes > 8192) { await reader.cancel(); throw new Error('Hooks status exceeds bounds') } text += new TextDecoder().decode(chunk.value, { stream: true }) }
    status = JSON.parse(text)
    if (status.version !== 1 || !['pending','failed','leased','lag_seconds'].every(name => typeof status[name] === 'number' && Number.isFinite(status[name]) && status[name] >= 0)) throw new Error('Invalid hooks status')
    log({ event: 'data_api_hooks_progress', batch: batch + 1, pending: status.pending, failed: status.failed, leased: status.leased, lag_seconds: status.lag_seconds })
    if (!status.pending || status.pending >= previous) break // backoff/lease: next scheduled invocation tries again
    previous = status.pending
  }
  return status
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) pumpHooks({ url: process.env.DATA_API_URL, key: process.env.DATA_API_SERVER_KEY }).catch(() => { console.error('Data API hooks worker failed; inspect the owner-only hook status.'); process.exitCode = 1 })
