import http from 'node:http'
import https from 'node:https'
import { createHash, createHmac } from 'node:crypto'
import pg from 'pg'
import { limits } from './config.mjs'
import { addResponseBytes } from './usage.mjs'

// File storage (ADR-20261021101510). Rows in <schema>.storage_objects record
// files; the owner's RLS policies on that table decide every upload, download
// and removal, checked through PostgREST as the caller. Bytes stream between
// the client and a bucket bound to this app; its credentials stay here.

export const storageTable = 'storage_objects'

const actions = { object: { PUT: 'upload', GET: 'download', HEAD: 'head', DELETE: 'remove' }, sign: { POST: 'sign' } }

// storageRoute parses /storage/v1/(object|sign)/BUCKET/NAME. Names are
// percent-encoded path segments: 1–1024 bytes, no control characters,
// backslashes, or empty, "." or ".." segments.
export function storageRoute(req, storage) {
  const match = /^\/storage\/v1\/(object|sign)\/([^/?#]+)\/([^?#]+)$/.exec(req.url)
  if (!match) return { status: 404, code: 'not_found' }
  const action = actions[match[1]][req.method]
  if (!action) return { status: 405, code: 'method_not_allowed' }
  const bucket = match[2]
  if (!storage.buckets.has(bucket)) return { status: 404, code: 'storage_bucket_not_found' }
  let segments
  try { segments = match[3].split('/').map(decodeURIComponent) } catch { return { status: 400, code: 'storage_invalid_name' } }
  const name = segments.join('/')
  if (Buffer.byteLength(name) > 1024 || segments.some(s => s === '' || s === '.' || s === '..' || s.includes('/')) || /[\x00-\x1f\x7f\\]/.test(name)) return { status: 400, code: 'storage_invalid_name' }
  return { action, bucket, name, write: action === 'upload' || action === 'remove' }
}

const contentTypePattern = /^[a-z0-9!#$&^_.+-]{1,127}\/[a-z0-9!#$&^_.+-]{1,127}(;[ -~]*)?$/i

// storageContentType accepts a media type up to 255 bytes, defaulting to
// application/octet-stream.
export function storageContentType(value) {
  if (value === undefined) return 'application/octet-stream'
  return typeof value === 'string' && value.length <= 255 && contentTypePattern.test(value) ? value : null
}

function rfc3986(value) {
  return encodeURIComponent(value).replace(/[!'()*]/g, c => `%${c.charCodeAt(0).toString(16).toUpperCase()}`)
}

const hmac = (key, value) => createHmac('sha256', key).update(value).digest()

function signingKey(secretAccessKey, date, region) {
  return hmac(hmac(hmac(hmac(`AWS4${secretAccessKey}`, date), region), 's3'), 'aws4_request')
}

// objectURL addresses a key in a binding's bucket, path-style unless the
// binding asks for virtual-hosted addressing.
export function objectURL(binding, key) {
  const path = key.split('/').map(rfc3986).join('/')
  const url = new URL(binding.endpoint)
  if (binding.virtualHosted) {
    url.hostname = `${binding.bucket}.${url.hostname}`
    url.pathname = `/${path}`
  } else {
    url.pathname = `${url.pathname.replace(/\/$/, '')}/${rfc3986(binding.bucket)}/${path}`
  }
  return url
}

// presign returns a SigV4 query-signed URL for method on url, valid for
// expires seconds and signing only the host header.
export function presign({ method, url, region, accessKeyId, secretAccessKey, expires, now = new Date() }) {
  const amzDate = now.toISOString().replace(/[-:]/g, '').replace(/\.\d{3}/, '')
  const date = amzDate.slice(0, 8)
  const scope = `${date}/${region}/s3/aws4_request`
  const params = {
    'X-Amz-Algorithm': 'AWS4-HMAC-SHA256',
    'X-Amz-Credential': `${accessKeyId}/${scope}`,
    'X-Amz-Date': amzDate,
    'X-Amz-Expires': String(expires),
    'X-Amz-SignedHeaders': 'host',
  }
  const query = Object.keys(params).sort().map(k => `${rfc3986(k)}=${rfc3986(params[k])}`).join('&')
  const canonical = [method, url.pathname, query, `host:${url.host}\n`, 'host', 'UNSIGNED-PAYLOAD'].join('\n')
  const toSign = ['AWS4-HMAC-SHA256', amzDate, scope, createHash('sha256').update(canonical).digest('hex')].join('\n')
  const signature = createHmac('sha256', signingKey(secretAccessKey, date, region)).update(toSign).digest('hex')
  return `${url.origin}${url.pathname}?${query}&X-Amz-Signature=${signature}`
}

// signHeaders signs a request with SigV4 in the Authorization header. Every
// header passed is signed; the payload is unsigned unless payloadHash is set.
export function signHeaders({ method, url, region, accessKeyId, secretAccessKey, headers = {}, payloadHash = 'UNSIGNED-PAYLOAD', now = new Date() }) {
  const amzDate = now.toISOString().replace(/[-:]/g, '').replace(/\.\d{3}/, '')
  const date = amzDate.slice(0, 8)
  const scope = `${date}/${region}/s3/aws4_request`
  const signed = { ...Object.fromEntries(Object.entries(headers).map(([k, v]) => [k.toLowerCase(), String(v).trim()])), host: url.host, 'x-amz-content-sha256': payloadHash, 'x-amz-date': amzDate }
  const names = Object.keys(signed).sort()
  const canonical = [method, url.pathname, url.searchParams.toString(), names.map(n => `${n}:${signed[n]}\n`).join(''), names.join(';'), payloadHash].join('\n')
  const toSign = ['AWS4-HMAC-SHA256', amzDate, scope, createHash('sha256').update(canonical).digest('hex')].join('\n')
  const signature = createHmac('sha256', signingKey(secretAccessKey, date, region)).update(toSign).digest('hex')
  delete signed.host
  return { ...signed, authorization: `AWS4-HMAC-SHA256 Credential=${accessKeyId}/${scope},SignedHeaders=${names.join(';')},Signature=${signature}` }
}

const storageTableQuery = `SELECT c.relkind AS kind, c.relrowsecurity AS rls,
 EXISTS (SELECT 1 FROM pg_trigger t JOIN pg_proc p ON p.oid=t.tgfoid
   WHERE t.tgrelid=c.oid AND t.tgname='storage_objects_guard' AND NOT t.tgisinternal AND t.tgenabled IN ('O','A')
   AND (t.tgtype & 31)=31 AND p.prosrc LIKE '%gregale_storage%') AS guarded
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname=$2`

// unsafeStorageTable explains why a catalog row cannot serve storage: the
// table must have RLS enabled and its guard trigger must fire before each row
// insert, update and delete (tgtype ROW|BEFORE|INSERT|DELETE|UPDATE).
export function unsafeStorageTable(row) {
  if (!row) return 'the storage_objects table is missing'
  if (row.kind !== 'r' && row.kind !== 'p') return 'storage_objects is not a table'
  if (row.rls !== true) return 'storage_objects needs row-level security enabled'
  if (row.guarded !== true) return 'storage_objects needs its enabled storage_objects_guard trigger'
  return null
}

export async function checkStorageTable(connection, schema) {
  const client = new pg.Client({ connectionString: connection, statement_timeout: limits.queryMs, connectionTimeoutMillis: limits.healthProbeMs })
  await client.connect()
  try {
    const { rows } = await client.query(storageTableQuery, [schema, storageTable])
    const unsafe = unsafeStorageTable(rows[0])
    if (unsafe) throw new Error(`File storage is not installed: ${unsafe}`)
  } finally { await client.end() }
}


// rows asks PostgREST about storage_objects as the caller.
function rows(ctx, { method, query, body, prefer, token, signal }) {
  const payload = body === undefined ? null : Buffer.from(JSON.stringify(body))
  const headers = { authorization: `Bearer ${token}`, accept: 'application/json', [method === 'GET' ? 'accept-profile' : 'content-profile']: ctx.config.storage.schema }
  if (prefer) headers.prefer = prefer
  if (payload) { headers['content-type'] = 'application/json'; headers['content-length'] = String(payload.length) }
  return new Promise(resolve => {
    const request = http.request({ hostname: '127.0.0.1', port: ctx.upstreamPort, path: `/${storageTable}?${query}`, method, headers, signal }, response => {
      const chunks = []
      let bytes = 0
      response.on('data', chunk => {
        bytes += chunk.length
        if (bytes > limits.outputBytes) { response.destroy(); resolve({ status: 0 }) } else chunks.push(chunk)
      })
      response.on('error', () => resolve({ status: 0 }))
      response.on('end', () => {
        let json = null
        try { json = JSON.parse(Buffer.concat(chunks).toString('utf8') || 'null') } catch { /* status decides */ }
        resolve({ status: response.statusCode, json })
      })
    })
    request.on('error', () => resolve({ status: 0 }))
    if (payload) request.end(payload)
    else request.end()
  })
}

// An eq filter takes the rest of the parameter as its value, so a percent-
// encoded name needs no quoting (PostgREST would compare the quotes too).
const rowFilter = route => `bucket=eq.${encodeURIComponent(route.bucket)}&name=eq.${encodeURIComponent(route.name)}`
const columns = 'select=bucket,name,owner,size,content_type,created_at,updated_at'

// refused answers a PostgREST refusal: RLS and the guard answer 401/403.
function refused(ctx, result) {
  if (result.status === 401 || result.status === 403) return ctx.problem(ctx.res, 403, 'storage_forbidden')
  if (result.status >= 400 && result.status < 500) return ctx.problem(ctx.res, 400, 'storage_rejected')
  return ctx.problem(ctx.res, 503, 'data_api_unavailable')
}

// transfer runs an object request after the caller is authorized: it releases
// the SQL admission slot and the request deadline, and takes one of the
// instance's transfer slots for at most storageTransferMs.
async function transfer(ctx, work) {
  if (ctx.transfers.active >= limits.storageTransfers) {
    ctx.res.setHeader('Retry-After', '1')
    return ctx.problem(ctx.res, 503, 'storage_busy')
  }
  ctx.transfers.active++
  ctx.release()
  ctx.deadline.stop()
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), limits.storageTransferMs)
  timer.unref()
  const abort = () => controller.abort()
  ctx.res.once('close', abort)
  try { return await work(controller.signal) } finally {
    clearTimeout(timer)
    ctx.res.removeListener('close', abort)
    ctx.transfers.active--
  }
}

function objectRequest(binding, method, key, { headers = {}, signal, body } = {}) {
  const url = objectURL(binding, key)
  const signed = signHeaders({ method, url, region: binding.region, accessKeyId: binding.accessKeyId, secretAccessKey: binding.secretAccessKey, headers })
  const client = url.protocol === 'https:' ? https : http
  return new Promise((resolve, reject) => {
    const request = client.request(url, { method, headers: signed, signal }, resolve)
    request.on('error', reject)
    if (body) body.pipe(request)
    else request.end()
  })
}

const drain = response => new Promise(resolve => { response.resume(); response.on('end', resolve); response.on('error', resolve) })

function sendJSON(res, status, body) {
  const output = JSON.stringify(body)
  res.writeHead(status, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' })
  addResponseBytes(res, Buffer.byteLength(output))
  res.end(output)
}

async function upload(ctx) {
  const { req, res, route, config } = ctx
  const size = Number(req.headers['content-length'])
  const contentType = storageContentType(req.headers['content-type'])
  if (!contentType) return ctx.problem(res, 400, 'storage_invalid_content_type')
  const row = { bucket: route.bucket, name: route.name, size, content_type: contentType }
  const upsert = { method: 'POST', query: `on_conflict=bucket,name&${columns}`, body: row }
  const dry = await rows(ctx, { ...upsert, prefer: 'resolution=merge-duplicates,return=representation,tx=rollback', token: await ctx.token(true), signal: ctx.deadline.signal })
  if (!ctx.deadline.check()) return
  if (dry.status < 200 || dry.status > 299) return refused(ctx, dry)
  const binding = config.storage.buckets.get(route.bucket)
  return transfer(ctx, async signal => {
    let sent = 0
    req.on('data', chunk => { sent += chunk.length })
    let stored
    try {
      stored = await objectRequest(binding, 'PUT', route.name, { headers: { 'content-length': size, 'content-type': contentType }, signal, body: req })
    } catch { return ctx.problem(res, 502, 'storage_backend_unavailable') }
    await drain(stored)
    if (stored.statusCode < 200 || stored.statusCode > 299 || sent !== size) return ctx.problem(res, 502, 'storage_backend_unavailable')
    const committed = await rows(ctx, { ...upsert, prefer: 'resolution=merge-duplicates,return=representation', token: await ctx.token(true), signal })
    if (committed.status < 200 || committed.status > 299 || !Array.isArray(committed.json) || committed.json.length !== 1) return ctx.problem(res, 503, 'storage_commit_failed')
    sendJSON(res, 200, committed.json[0])
  })
}

// readRow finds the caller's row for a download, a HEAD or a signed URL. A
// row the caller may not see answers like a missing one.
async function readRow(ctx) {
  const found = await rows(ctx, { method: 'GET', query: `${rowFilter(ctx.route)}&${columns}`, token: await ctx.token(false), signal: ctx.deadline.signal })
  if (!ctx.deadline.check()) return null
  if (found.status !== 200 || !Array.isArray(found.json)) { refused(ctx, found); return null }
  if (found.json.length !== 1) { ctx.problem(ctx.res, 404, 'storage_not_found'); return null }
  return found.json[0]
}

// Served files never run as the Data API's origin.
const servedHeaders = { 'Cache-Control': 'private, no-store', 'Content-Security-Policy': "sandbox; default-src 'none'", 'X-Content-Type-Options': 'nosniff' }

async function download(ctx) {
  const row = await readRow(ctx)
  if (!row) return
  const { req, res, route, config } = ctx
  if (route.action === 'head') {
    res.writeHead(200, { ...servedHeaders, 'Content-Type': row.content_type, 'Content-Length': String(row.size), 'Accept-Ranges': 'bytes' })
    return res.end()
  }
  const range = typeof req.headers.range === 'string' && /^bytes=\d*-\d*$/.test(req.headers.range) ? req.headers.range : null
  const binding = config.storage.buckets.get(route.bucket)
  return transfer(ctx, async signal => {
    let object
    try { object = await objectRequest(binding, 'GET', route.name, { headers: range ? { range } : {}, signal }) } catch { return ctx.problem(res, 502, 'storage_backend_unavailable') }
    if (object.statusCode === 404) { await drain(object); return ctx.problem(res, 404, 'storage_not_found') }
    if (object.statusCode === 416) { await drain(object); return ctx.problem(res, 416, 'storage_range_not_satisfiable') }
    if (object.statusCode !== 200 && object.statusCode !== 206) { await drain(object); return ctx.problem(res, 502, 'storage_backend_unavailable') }
    const headers = { ...servedHeaders, 'Content-Type': row.content_type, 'Accept-Ranges': 'bytes' }
    for (const [name, header] of [['content-length', 'Content-Length'], ['content-range', 'Content-Range'], ['etag', 'ETag'], ['last-modified', 'Last-Modified']]) {
      if (object.headers[name]) headers[header] = object.headers[name]
    }
    res.writeHead(object.statusCode, headers)
    await new Promise(resolve => {
      object.on('data', chunk => addResponseBytes(res, chunk.length))
      object.on('end', resolve)
      object.on('error', () => { res.destroy(); resolve() })
      object.pipe(res)
    })
  })
}

async function sign(ctx) {
  const row = await readRow(ctx)
  if (!row) return
  const binding = ctx.config.storage.buckets.get(ctx.route.bucket)
  const now = new Date()
  const url = presign({ method: 'GET', url: objectURL(binding, ctx.route.name), region: binding.region, accessKeyId: binding.accessKeyId, secretAccessKey: binding.secretAccessKey, expires: limits.storageSignSeconds, now })
  sendJSON(ctx.res, 200, { url, expires_at: new Date(now.getTime() + limits.storageSignSeconds * 1000).toISOString() })
}

async function remove(ctx) {
  const { res, route, config } = ctx
  const query = `${rowFilter(route)}&${columns}`
  const dry = await rows(ctx, { method: 'DELETE', query, prefer: 'return=representation,tx=rollback', token: await ctx.token(true), signal: ctx.deadline.signal })
  if (!ctx.deadline.check()) return
  if (dry.status !== 200 || !Array.isArray(dry.json)) return refused(ctx, dry)
  if (dry.json.length !== 1) return ctx.problem(res, 404, 'storage_not_found')
  const binding = config.storage.buckets.get(route.bucket)
  return transfer(ctx, async signal => {
    let deleted
    try { deleted = await objectRequest(binding, 'DELETE', route.name, { signal }) } catch { return ctx.problem(res, 502, 'storage_backend_unavailable') }
    await drain(deleted)
    if (deleted.statusCode !== 204 && deleted.statusCode !== 200 && deleted.statusCode !== 404) return ctx.problem(res, 502, 'storage_backend_unavailable')
    const committed = await rows(ctx, { method: 'DELETE', query, prefer: 'return=representation', token: await ctx.token(true), signal })
    if (committed.status !== 200) return ctx.problem(res, 503, 'storage_commit_failed')
    res.writeHead(204, { 'Cache-Control': 'no-store' })
    res.end()
  })
}

// handleStorage serves an authorized storage request. ctx carries the
// request, the parsed route, token(write) minting the caller's internal
// token, the deadline, release() for the admission slot and the server's
// transfer counter.
export function handleStorage(ctx) {
  switch (ctx.route.action) {
    case 'upload': return upload(ctx)
    case 'download':
    case 'head': return download(ctx)
    case 'sign': return sign(ctx)
    case 'remove': return remove(ctx)
  }
}

// storageAnonymousTarget lets visitors download and sign only when the
// owner lists storage_objects as public; RLS still decides the rows.
export function storageAnonymousTarget(route, config) {
  if (route.write) return { code: 'token_required' }
  const listed = config.publicRead?.some(r => r.schema === config.storage.schema && r.relation === storageTable)
  return listed ? {} : { code: 'public_read_not_allowed' }
}

// storageServerKeyAllows applies a restricted key: its relations must list
// storage_objects, and a read-only key may only download.
export function storageServerKeyAllows(route, permissions, config) {
  if (permissions.access === 'read' && route.write) return false
  if (permissions.relations && !permissions.relations.some(r => r.schema === config.storage.schema && (r.name ?? r.relation) === storageTable)) return false
  return true
}
