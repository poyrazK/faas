export type DataResponseInfo = Readonly<{
  requestId: string | null
  status: number
  /** Client time from token lookup to response headers; excludes body consumption. */
  durationMs: number
}>

export type DataClientBaseOptions = {
  url: string
  fetch?: typeof globalThis.fetch
  /** Opt in to an exact serving-contract match before each REST/RPC operation. */
  expectedSchemaFingerprint?: string
  /** Called per HTTP response. Callback failures do not affect requests. */
  onResponse?: (response: DataResponseInfo) => void | Promise<void>
}

export type DataClientOptions = DataClientBaseOptions & (
  | { accessToken: string | (() => string | Promise<string>); anonymous?: false }
  /**
   * Requests without a token are anonymous. The Data API answers them only for
   * relations the owner made public and GET calls of public RPCs
   * (rpc(name, args, { get: true })). A token, when there is one, is sent as usual.
   */
  | { anonymous: true; accessToken?: string | null | (() => string | null | Promise<string | null>) }
)

const requestIdPattern = /^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/i

export function requestIdOf(response: Response): string | null {
  const id = response.headers.get('X-Request-Id')
  return id?.length === 36 && requestIdPattern.test(id) ? id : null
}

async function resolveToken<Token>(value: Token | Promise<Token>, signal?: AbortSignal | null): Promise<Token> {
  if (!signal) return value
  signal.throwIfAborted()
  return new Promise((resolve, reject) => {
    const aborted = () => { signal.removeEventListener('abort', aborted); reject(signal.reason) }
    signal.addEventListener('abort', aborted, { once: true })
    Promise.resolve(value).then(resolve, reject).finally(() => signal.removeEventListener('abort', aborted))
  })
}

/** The Data API's origin (and mount path), without /rest/v1, from a client URL. */
export function dataAPIBase(value: string): string {
  const url = new URL(value)
  if (url.username || url.password || url.search || url.hash) throw new Error('Data API URL must not contain credentials, a query or a fragment')
  if (url.protocol !== 'https:' && !(url.protocol === 'http:' && ['localhost', '127.0.0.1', '[::1]'].includes(url.hostname))) throw new Error('Data API requires HTTPS')
  const base = url.toString().replace(/\/$/, '')
  return base.endsWith('/rest/v1') ? base.slice(0, -'/rest/v1'.length) : base
}

/** A fetch that adds the application token, or none for an anonymous client. */
export function authenticatedFetch(options: DataClientOptions): typeof globalThis.fetch {
  if (options.anonymous !== undefined && typeof options.anonymous !== 'boolean') throw new Error('anonymous must be a boolean')
  const anonymous = options.anonymous === true
  const fetcher = options.fetch ?? globalThis.fetch
  return async (input, init) => {
    const started = performance.now()
    init?.signal?.throwIfAborted()
    const source = options.accessToken
    const token = source === undefined || source === null ? null : await resolveToken(typeof source === 'function' ? source() : source, init?.signal)
    init?.signal?.throwIfAborted()
    // Only an explicit anonymous client sends no token, and only for an absent
    // one; an empty or malformed token is an application bug either way.
    const absent = anonymous && (token === null || token === undefined)
    if (!absent && (typeof token !== 'string' || !token || /\s/.test(token))) throw new Error('A valid application access token is required')
    const headers = new Headers(init?.headers)
    if (absent) headers.delete('Authorization')
    else headers.set('Authorization', `Bearer ${token}`)
    const response = await fetcher(input, { ...init, headers, credentials: 'omit', redirect: 'error' })
    if (options.onResponse) {
      const info: DataResponseInfo = Object.freeze({
        requestId: requestIdOf(response),
        status: response.status,
        durationMs: performance.now() - started,
      })
      try { void Promise.resolve(options.onResponse(info)).catch(() => {}) } catch { /* Diagnostics must not affect data access. */ }
    }
    return response
  }
}
