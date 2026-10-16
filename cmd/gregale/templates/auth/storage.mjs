import { createHash, createHmac } from 'node:crypto'

// Object access for application Auth (ADR-1190 phase 5). Buckets bound to
// the Auth app with `gregale add bucket NAME --app AUTH_APP` arrive as
// GREGALE_S3_<NAME>_* secrets. The front signs short-lived SigV4 URLs for
// them only after authorize() allows the caller; the credentials never leave
// the instance.

const suffixes = ['ENDPOINT', 'REGION', 'BUCKET', 'ACCESS_KEY_ID', 'SECRET_ACCESS_KEY']

// storageBindings maps a binding name (the prefix after GREGALE_S3_,
// lower-cased) to its credentials. The name is also the policy resource kind.
export function storageBindings(env) {
  const bindings = new Map()
  for (const key of Object.keys(env)) {
    const match = /^GREGALE_S3_([A-Z0-9_]+)_ENDPOINT$/.exec(key)
    if (!match) continue
    const prefix = `GREGALE_S3_${match[1]}`
    const name = match[1].toLowerCase()
    if (!/^[a-z][a-z0-9_]{0,63}$/.test(name) || suffixes.some(s => !env[`${prefix}_${s}`])) continue
    let endpoint
    try { endpoint = new URL(env[`${prefix}_ENDPOINT`]) } catch { continue }
    if (endpoint.protocol !== 'https:' && !['localhost', '127.0.0.1'].includes(endpoint.hostname)) continue
    bindings.set(name, Object.freeze({
      endpoint, region: env[`${prefix}_REGION`], bucket: env[`${prefix}_BUCKET`],
      accessKeyId: env[`${prefix}_ACCESS_KEY_ID`], secretAccessKey: env[`${prefix}_SECRET_ACCESS_KEY`],
      virtualHosted: env[`${prefix}_ADDRESSING_STYLE`] === 'virtual',
    }))
  }
  return bindings
}

const uuid = '[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}'

// objectResource checks an object key and names it for authorize(). Every
// key belongs to an organization: org/<uuid>/<path>.
export function objectResource(bucket, key) {
  if (typeof key !== 'string' || Buffer.byteLength(key) > 1024) return null
  const match = new RegExp(`^org/(${uuid})/(.+)$`).exec(key)
  if (!match || /[\x00-\x1f\x7f\\]/.test(key)) return null
  const segments = match[2].split('/')
  if (segments.some(s => s === '' || s === '.' || s === '..')) return null
  return `org/${match[1]}/${bucket}/${match[2]}`
}

function rfc3986(value) {
  return encodeURIComponent(value).replace(/[!'()*]/g, c => `%${c.charCodeAt(0).toString(16).toUpperCase()}`)
}

const hmac = (key, value) => createHmac('sha256', key).update(value).digest()

// presign returns a SigV4 query-signed URL (AWS "presigned URL" form) for
// method on url, valid for expires seconds and signing only the host header.
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
  const canonical = [method, url.pathname, query, `host:${url.host}`, '', 'host', 'UNSIGNED-PAYLOAD'].join('\n')
  const toSign = ['AWS4-HMAC-SHA256', amzDate, scope, createHash('sha256').update(canonical).digest('hex')].join('\n')
  const key = hmac(hmac(hmac(hmac(`AWS4${secretAccessKey}`, date), region), 's3'), 'aws4_request')
  return `${url.origin}${url.pathname}?${query}&X-Amz-Signature=${createHmac('sha256', key).update(toSign).digest('hex')}`
}

export function objectURL(binding, key) {
  const path = key.split('/').map(rfc3986).join('/')
  const base = binding.endpoint
  if (binding.virtualHosted) return new URL(`${base.protocol}//${binding.bucket}.${base.host}/${path}`)
  return new URL(`${base.origin}${base.pathname.replace(/\/$/, '')}/${rfc3986(binding.bucket)}/${path}`)
}
