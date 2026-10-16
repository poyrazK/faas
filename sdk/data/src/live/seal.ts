/**
 * Sealed live queries (ADR-1384). A backend seals a query, its parameters and
 * its database with the per-database live secret; the browser presents the
 * seal to subscribe. Sealing is local Web Crypto (Node 22, browsers, edge
 * runtimes) and makes no network call. The format matches pkg/livequery/seal
 * byte for byte (testdata/vector.json there).
 */

export const LIVE_SEAL_DEFAULT_TTL_SECONDS = 60
export const LIVE_SEAL_MAX_TTL_SECONDS = 300
const SQL_MAX_BYTES = 32 << 10
const PARAMS_MAX = 256
const PARAM_MAX_BYTES = 16 << 10
const PAYLOAD_MAX_BYTES = 48 << 10
const DATABASE_MAX_BYTES = 128

export class LiveSealError extends Error {
  constructor(message: string) { super(message); this.name = 'LiveSealError' }
}

/** A query with PostgreSQL text parameters, as the sql tag produces. */
export type LiveQueryText = Readonly<{ text: string; params: ReadonlyArray<string | null> }>

export type LiveSealOptions = {
  /** Seal lifetime; default 60 seconds, at most 300. */
  ttlSeconds?: number
  /** Overrides the clock (seconds since the epoch), for tests. */
  now?: number
}

export type LiveSealer = Readonly<{
  /** The key ID the secret seals under. */
  kid: string
  seal(query: LiveQueryText, options?: LiveSealOptions): Promise<string>
}>

type Secret = { kid: string; key: CryptoKey }

const encoder = new TextEncoder()

function base64url(bytes: Uint8Array): string {
  let binary = ''
  for (const b of bytes) binary += String.fromCharCode(b)
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

function fromBase64url(text: string): Uint8Array | undefined {
  if (!/^[A-Za-z0-9_-]*$/.test(text)) return undefined
  const padded = text.replace(/-/g, '+').replace(/_/g, '/') + '='.repeat((4 - (text.length % 4)) % 4)
  try {
    return Uint8Array.from(atob(padded), (c) => c.charCodeAt(0))
  } catch {
    return undefined
  }
}

async function parseSecret(secret: string): Promise<Secret> {
  const match = /^glsk1_([0-9a-f]{16})_([A-Za-z0-9_-]+)$/.exec(secret)
  const material = match ? fromBase64url(match[2]) : undefined
  if (!match || !material || material.length !== 32) throw new LiveSealError('invalid live secret')
  const ikm = await crypto.subtle.importKey('raw', material as BufferSource, 'HKDF', false, ['deriveKey'])
  const key = await crypto.subtle.deriveKey(
    { name: 'HKDF', hash: 'SHA-256', salt: new Uint8Array(), info: encoder.encode('gregale live seal v1') },
    ikm, { name: 'AES-GCM', length: 256 }, false, ['encrypt'],
  )
  return { kid: match[1], key }
}

function checkText(what: string, value: string, limit: number): void {
  const bytes = encoder.encode(value).length
  if (bytes > limit) throw new LiveSealError(`${what} is ${bytes} bytes, limit ${limit}`)
  if (value.includes('\u0000')) throw new LiveSealError(`${what} contains a NUL character`)
  if (/[\uD800-\uDFFF]/.test(value.replace(/[\uD800-\uDBFF][\uDC00-\uDFFF]/g, ''))) throw new LiveSealError(`${what} is not valid Unicode`)
}

/**
 * Seals with the database's live secret (GREGALE_LIVE_SECRET) for that
 * database (GREGALE_LIVE_DATABASE). Keep the secret on the server.
 */
export async function createLiveSealer(options: { secret: string; database: string }): Promise<LiveSealer> {
  const secret = await parseSecret(options.secret)
  const database = options.database
  if (typeof database !== 'string' || database === '') throw new LiveSealError('a database is required')
  checkText('database', database, DATABASE_MAX_BYTES)
  return Object.freeze({
    kid: secret.kid,
    async seal(query: LiveQueryText, sealOptions: LiveSealOptions = {}): Promise<string> {
      const ttl = sealOptions.ttlSeconds ?? LIVE_SEAL_DEFAULT_TTL_SECONDS
      if (!Number.isInteger(ttl) || ttl < 1 || ttl > LIVE_SEAL_MAX_TTL_SECONDS) throw new LiveSealError(`seal lifetime ${ttl} s, limit ${LIVE_SEAL_MAX_TTL_SECONDS} s`)
      if (typeof query.text !== 'string' || query.text.trim() === '') throw new LiveSealError('SQL is required')
      checkText('SQL', query.text, SQL_MAX_BYTES)
      if (query.params.length > PARAMS_MAX) throw new LiveSealError(`${query.params.length} parameters, limit ${PARAMS_MAX}`)
      query.params.forEach((p, i) => { if (p !== null) checkText(`parameter ${i + 1}`, p, PARAM_MAX_BYTES) })
      const iat = Math.floor(sealOptions.now ?? Date.now() / 1000)
      const plaintext = JSON.stringify({ db: database, sql: query.text, params: [...query.params], iat, exp: iat + ttl })
      const plain = encoder.encode(plaintext)
      if (plain.length > PAYLOAD_MAX_BYTES) throw new LiveSealError(`sealed query is ${plain.length} bytes, limit ${PAYLOAD_MAX_BYTES}`)
      const nonce = crypto.getRandomValues(new Uint8Array(12))
      return sealWithNonce(secret, plain, nonce)
    },
  })
}

async function sealWithNonce(secret: Secret, plain: Uint8Array, nonce: Uint8Array): Promise<string> {
  const header = `gls1.${secret.kid}`
  const sealed = new Uint8Array(await crypto.subtle.encrypt(
    { name: 'AES-GCM', iv: nonce as BufferSource, additionalData: encoder.encode(header) }, secret.key, plain as BufferSource))
  const body = new Uint8Array(nonce.length + sealed.length)
  body.set(nonce); body.set(sealed, nonce.length)
  return `${header}.${base64url(body)}`
}

/** Test hook: the known-answer vector seals with a fixed nonce. */
export async function sealWithFixedNonceForTest(secretText: string, plaintext: string, nonce: Uint8Array): Promise<string> {
  return sealWithNonce(await parseSecret(secretText), encoder.encode(plaintext), nonce)
}
