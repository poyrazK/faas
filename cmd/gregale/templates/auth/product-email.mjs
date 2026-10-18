import { randomBytes, createHash } from 'node:crypto'
import { limits } from './config.mjs'

const uuid = value => typeof value === 'string' && /^[a-f0-9]{8}-(?:[a-f0-9]{4}-){3}[a-f0-9]{12}$/.test(value) && value !== '00000000-0000-0000-0000-000000000000'
export class ProductEmailError extends Error {
  constructor(status, code) { super(code); this.status = status; this.code = code }
}
const failure = (status, code) => new ProductEmailError(status, code)

export const emailPath = path => /^\/orgs\/[^/]+\/email-recipient(?:\/|$)/.test(path)

// The worker scheme is deliberately incompatible with GoTrue JWTs. It cannot
// log in, enumerate users, change membership, or invoke any other Auth API.
export async function resolveEmailRecipient(pool, method, path, authorization) {
  const parts = path.split('/').filter(Boolean)
  if (method !== 'GET' || parts.length !== 4 || !uuid(parts[1]) || !uuid(parts[3])) throw failure(404, 'not_found')
  const token = authorization?.match(/^Product-Recipient ([A-Za-z0-9_-]{43})$/)?.[1]
  if (!token || Buffer.from(token, 'base64url').toString('base64url') !== token) throw failure(401, 'not_authenticated')
  const hashed = createHash('sha256').update(Buffer.from(token, 'base64url')).digest()
  const { rows } = await pool.query('SELECT * FROM gregale_auth.product_email_recipient($1,$2,$3)', [parts[1], parts[3], hashed])
  if (rows.length !== 1) throw failure(403, 'forbidden')
  return [200, { ...rows[0], product_id: parts[3], protocol: 1 }]
}

export async function emailConsent(run, method, parts, body) {
  if (parts.length !== 3 || !uuid(parts[1])) throw failure(404, 'not_found')
  if (method === 'GET') {
    const { rows } = await run(c => c.query('SELECT * FROM gregale_auth.product_email_self($1)', [parts[1]]))
    return [200, { ...rows[0], protocol: 1 }]
  }
  if (method !== 'POST' || !body || Object.keys(body).length !== 1 || !uuid(body.product_id)) throw failure(400, 'invalid_request')
  const token = randomBytes(32).toString('base64url')
  const hashed = createHash('sha256').update(Buffer.from(token, 'base64url')).digest()
  const { rows } = await run(c => c.query('SELECT * FROM gregale_auth.product_email_consent($1,$2,$3,$4)', [parts[1], body.product_id, hashed, limits.emailGrantSeconds]))
  return [200, { ...rows[0], product_id: body.product_id, token, protocol: 1 }]
}
