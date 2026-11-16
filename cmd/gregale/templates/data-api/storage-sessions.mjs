import { createHash } from 'node:crypto'
import { Readable } from 'node:stream'
import { limits } from './config.mjs'

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/
const checksum = /^[0-9a-f]{64}$/
export const uploadTable = 'gregale_storage_uploads'
export function uploadSessionRoute(req, storage) {
  const match = /^\/storage\/v1\/upload\/([a-z][a-z0-9_]{0,62})(?:\/([^/]+)(?:\/(complete|parts\/[1-9][0-9]?))?)?$/.exec(req.url)
  if (!match || (match[2] && !uuid.test(match[2]))) return { status: 404, code: 'not_found' }
  if (!storage.buckets.has(match[1])) return { status: 404, code: 'storage_bucket_not_found' }
  const action = !match[2] && req.method === 'POST' ? 'sessionStart'
    : match[2] && !match[3] ? ({ GET: 'sessionStatus', DELETE: 'sessionAbort' })[req.method]
      : match[3] === 'complete' && req.method === 'POST' ? 'sessionComplete'
        : match[3]?.startsWith('parts/') && req.method === 'PUT' ? 'sessionPart' : null
  return action ? { action, bucket: match[1], id: match[2], part: match[3]?.startsWith('parts/') ? Number(match[3].slice(6)) : undefined, write: true } : { status: 405, code: 'method_not_allowed' }
}

// Scope sessions to the complete verified authorization context, including
// tenant claims, rather than just sub. Token refresh timestamps can change.
export function storageActor(claims) {
  const stable = value => Array.isArray(value) ? value.map(stable) : value && typeof value === 'object' ? Object.fromEntries(Object.keys(value).sort().map(key => [key, stable(value[key])])) : value
  const transient = new Set(['exp','iat','nbf','gregale_storage','gregale_storage_actor','gregale_sql_deadline','gregale_refresh_hash','gregale_serving_fingerprint'])
  const kept = Object.fromEntries(Object.entries(claims).filter(([key]) => !transient.has(key)))
  return createHash('sha256').update(JSON.stringify(stable(kept))).digest('hex')
}

async function body(req, max, signal) {
  const chunks = []; let size = 0
  signal?.throwIfAborted()
  const abort = () => req.destroy(new Error('transfer_aborted'))
  signal?.addEventListener('abort', abort, { once: true })
  try {
    for await (const chunk of req) { size += chunk.length; if (size > max) throw new Error('body_limit'); chunks.push(chunk) }
    signal?.throwIfAborted()
    return Buffer.concat(chunks)
  } finally { signal?.removeEventListener('abort', abort) }
}

export async function handleUploadSession(ctx, helpers) {
  const { rows, transfer, objectRequest, registerObject, discard, drain, sendJSON, publicRow, newObjectKey, validateName } = helpers
  const { route, req, res, config } = ctx
  const binding = config.storage.buckets.get(route.bucket)
  const rpc = async (name, payload, signal = ctx.deadline.signal) => rows(ctx, { resource: `rpc/gregale_storage_${name}`, method: 'POST', body: payload, token: await ctx.token(true), signal })
  const get = async (signal = ctx.deadline.signal) => rows(ctx, { resource: uploadTable, method: 'GET', query: `id=eq.${route.id}&bucket=eq.${route.bucket}`, token: await ctx.token(true), signal })
  const session = async signal => { const result = await get(signal); return result.status === 200 && Array.isArray(result.json) && result.json.length === 1 ? result.json[0] : null }
  const answer = s => sendJSON(res, 200, { id: s.id, bucket: s.bucket, name: s.name, size: s.size, content_type: s.content_type, state: s.state, expires_at: s.expires_at,
    chunk_size: limits.storageChunkBytes, parts: Object.entries(s.parts).map(([number, part]) => ({ number: Number(number), size: part.size, sha256: part.sha256 })).sort((a,b) => a.number-b.number), ...(s.receipt ? { object: publicRow(s.receipt) } : {}) })
  const failed = result => ctx.problem(res, [400,401,403,409,410,412,429].includes(result.status) ? result.status : 503,
    ({ 400: 'storage_rejected', 401: 'storage_forbidden', 403: 'storage_forbidden', 404: 'storage_sessions_unavailable', 409: 'storage_session_conflict', 410: 'storage_session_history_retired', 412: 'storage_version_conflict', 429: 'storage_session_limit' })[result.status] ?? 'storage_commit_unknown')
  if (route.action === 'sessionStart') {
    let input
    try { input = JSON.parse((await body(req, 4096, ctx.deadline.signal)).toString()) } catch { return ctx.problem(res, 400, 'storage_invalid_session') }
    if (!input || typeof input !== 'object' || Array.isArray(input) || Object.keys(input).some(key => !['id','name','size','content_type','expected_version'].includes(key)) || !uuid.test(input.id) || !validateName(input.name) || !Number.isSafeInteger(input.size) || input.size < 0 || input.size > config.storage.maxObjectBytes || !helpers.storageContentType(input.content_type) || (input.expected_version !== undefined && input.expected_version !== null && !/^[0-9a-f]{32}$/.test(input.expected_version))) return ctx.problem(res, 400, 'storage_invalid_session')
    const row = { bucket: route.bucket, name: input.name, size: input.size, content_type: input.content_type ?? 'application/octet-stream', object_key: newObjectKey() }
    const dry = await rows(ctx, { method: 'POST', query: 'on_conflict=bucket,name', body: row, prefer: 'resolution=merge-duplicates,tx=rollback', token: await ctx.token(true), signal: ctx.deadline.signal })
    if (dry.status < 200 || dry.status >= 300) return failed(dry)
    const result = await rpc('start', { p_id: input.id, p_bucket: row.bucket, p_name: row.name, p_size: row.size, p_content_type: row.content_type, p_key: row.object_key, p_expected: input.expected_version === null ? 'absent' : input.expected_version ?? null })
    if (result.status !== 200 || !result.json?.id) return failed(result)
    return answer(result.json)
  }
  const found = await session()
  if (!found) return ctx.problem(res, 404, 'storage_session_not_found')
  if (route.action === 'sessionStatus') return answer(found)
  if (route.action === 'sessionAbort') {
    const result = await rpc('abort', { p_id: route.id })
    if (result.status !== 200 || result.json?.state !== 'aborted') return failed(result)
    return transfer(ctx, async () => { for (const key of [result.json.object_key, ...Object.values(result.json.parts).map(p => p.key)]) await discard(binding, key); return answer(result.json) })
  }
  if (found.state !== 'complete') {
    if (found.size > config.storage.maxObjectBytes) return ctx.problem(res, 413, 'storage_object_too_large')
    const dry = await rows(ctx, { method: 'POST', query: 'on_conflict=bucket,name', body: { bucket: found.bucket, name: found.name, size: found.size, content_type: found.content_type, object_key: found.object_key }, prefer: 'resolution=merge-duplicates,tx=rollback', token: await ctx.token(true), signal: ctx.deadline.signal })
    if (dry.status < 200 || dry.status >= 300) return failed(dry)
  }
  if (route.action === 'sessionPart') {
    const expectedSize = Math.min(limits.storageChunkBytes, found.size - (route.part - 1) * limits.storageChunkBytes)
    const sha = req.headers['x-content-sha256']
    if (found.state !== 'uploading' || route.part > Math.ceil(found.size / limits.storageChunkBytes) || expectedSize <= 0 || Number(req.headers['content-length']) !== expectedSize || !checksum.test(sha ?? '')) return ctx.problem(res, 409, 'storage_invalid_part')
    const previous = found.parts[String(route.part)]
    if (previous && previous.sha256 !== sha) return ctx.problem(res, 409, 'storage_part_conflict')
    return transfer(ctx, async signal => {
      let bytes
      try { bytes = await body(req, limits.storageChunkBytes, signal) } catch { return ctx.problem(res, 400, 'storage_invalid_part') }
      if (bytes.length !== expectedSize || createHash('sha256').update(bytes).digest('hex') !== sha) return ctx.problem(res, 400, 'storage_checksum_mismatch')
      if (previous) return answer(found)
      const key = newObjectKey()
      if (!await registerObject(ctx,key,signal)) return ctx.problem(res,503,'storage_maintenance_unavailable')
      try { const stored = await objectRequest(binding, 'PUT', key, { body: bytes, headers: { 'content-length': bytes.length, 'content-type': 'application/octet-stream' }, signal }); await drain(stored); if (stored.statusCode < 200 || stored.statusCode >= 300) throw new Error('store') }
      catch { await discard(binding, key); return ctx.problem(res, 502, 'storage_backend_unavailable') }
      const result = await rpc('part', { p_id: route.id, p_number: route.part, p_size: bytes.length, p_sha256: sha, p_key: key }, signal)
      if (result.status !== 200 || !result.json?.id) { if (result.status >= 400 && result.status < 500) await discard(binding,key); return failed(result) }
      if (result.json.parts[String(route.part)].key !== key) await discard(binding, key)
      return answer(result.json)
    })
  }
  const prepared = await rpc('prepare', { p_id: route.id })
  if (prepared.status !== 200 || !prepared.json?.id) return failed(prepared)
  const plan = prepared.json
  if (plan.state === 'complete') return answer(plan)
  return transfer(ctx, async signal => {
    if (!await registerObject(ctx,plan.object_key,signal)) return ctx.problem(res,503,'storage_maintenance_unavailable')
    // Immutable, checksum-verified chunks are composed through a bounded
    // stream. No local disk or process state is needed to resume after park.
    const stream = Readable.from((async function* () {
      for (const [,part] of Object.entries(plan.parts).sort(([a],[b]) => Number(a)-Number(b))) {
        const chunk = await objectRequest(binding, 'GET', part.key, { signal })
        if (chunk.statusCode !== 200) { await drain(chunk); throw new Error('missing_part') }
        let size = 0; const hash = createHash('sha256')
        for await (const bytes of chunk) { size += bytes.length; if (size > part.size) throw new Error('invalid_part'); hash.update(bytes); yield bytes }
        if (size !== part.size || hash.digest('hex') !== part.sha256) throw new Error('invalid_part')
      }
    })())
    try {
      const stored = await objectRequest(binding, 'PUT', plan.object_key, { body: stream, headers: { 'content-length': plan.size, 'content-type': plan.content_type }, signal })
      await drain(stored)
      if (stored.statusCode < 200 || stored.statusCode >= 300) throw new Error('store')
    } catch { const recovered = await session(AbortSignal.timeout(limits.queryMs)); if (recovered?.state === 'complete') return answer(recovered); return ctx.problem(res, 502, 'storage_backend_unavailable') }
    const committed = await rpc('finish', { p_id: route.id }, signal)
    if (committed.status !== 200 || committed.json?.state !== 'complete') {
      const recovered = await session(AbortSignal.timeout(limits.queryMs))
      if (recovered?.state === 'complete') return answer(recovered)
      return failed(committed)
    }
    // Keep chunks until the receipt is durable. A concurrent finisher can
    // recover from this receipt if cleanup interrupts its duplicate stream.
    for (const part of Object.values(plan.parts)) await discard(binding, part.key)
    return answer(committed.json)
  })
}
