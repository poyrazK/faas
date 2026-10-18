import { cursorFrom, cursorPage, type CursorOrder, type CheckedOrder, type ReadQuery } from './cursor.js'

/** Derive this scope on the server from verified identity/tenant/permission claims. */
export type CursorScope = Readonly<{ identity: string; schemaFingerprint: string }>
export type CursorPagerOptions = Readonly<{ key: Uint8Array | CryptoKey; ttlMs?: number; maxPageSize?: number; now?: () => number }>
export type CursorPageResult<Query> = { result: Awaited<Query>; nextCursor: string | null; previousCursor: string | null; hasMore: boolean; direction: 'forward' | 'backward' }
type Scalar = string | number | boolean | null
type SelectedRow<Query> = Awaited<Query> extends { data: infer Data } ? NonNullable<Data> extends readonly (infer Row)[] ? Row : never : never
type SelectedOrder<Query, Order extends readonly CursorOrder[]> = { [Index in keyof Order]: Order[Index]['column'] extends keyof SelectedRow<Query> ? SelectedRow<Query>[Order[Index]['column']] extends Scalar ? Order[Index] : never : never }
type Builder<Query> = { order(column: string, options: { ascending: boolean; nullsFirst: boolean }): Query; limit(count: number): Query; or(filters: string): Query }
type Payload = { v: 1; binding: string; issued: number; expires: number; values: Scalar[] }
export class CursorPaginationError extends Error {
  constructor(public readonly code: 'invalid_options' | 'invalid_cursor' | 'expired_cursor' | 'incompatible_cursor' | 'invalid_response', message: string) { super(message); this.name = 'CursorPaginationError' }
}
const fail = (code: CursorPaginationError['code'], message: string): never => { throw new CursorPaginationError(code, message) }
const encoder = new TextEncoder(), decoder = new TextDecoder('utf-8', { fatal: true })
const additionalData = encoder.encode('gregale.data.cursor.v1')
function base64(bytes: Uint8Array): string { return btoa(String.fromCharCode(...bytes)).replaceAll('+', '-').replaceAll('/', '_').replace(/=+$/, '') }
function unbase64(value: string): Uint8Array {
  if (!/^[A-Za-z0-9_-]+$/.test(value)) return fail('invalid_cursor', 'Invalid continuation token')
  const bytes = Uint8Array.from(atob(value.replaceAll('-', '+').replaceAll('_', '/')), character => character.charCodeAt(0))
  if (base64(bytes) !== value) return fail('invalid_cursor', 'Invalid continuation token')
  return bytes
}
function timestamp(now: () => number): number { const time = now(); if (!Number.isSafeInteger(time) || time < 0) return fail('invalid_options', 'Invalid cursor clock'); return time }
function queryBinding(query: unknown, scope: CursorScope, order: readonly CursorOrder[], size: number): string {
  // Inspect the pinned PostgREST builder before pagination mutates it. Fail closed
  // on unsupported builder shapes; this binds actual filters, projection and schema.
  const builder = query as { url?: unknown; method?: unknown; schema?: unknown; headers?: unknown }
  if (!(builder.url instanceof URL) || builder.method !== 'GET' || builder.headers === undefined) return fail('invalid_options', 'Use a fresh selected GET query')
  const url = builder.url
  if (['order', 'limit', 'offset'].some(key => url.searchParams.has(key))) return fail('invalid_options', 'Apply ordering and page size only through the pager')
  const headers = new Headers(builder.headers as HeadersInit)
  if (headers.get('Accept')?.includes('object+json') || headers.has('Range')) return fail('invalid_options', 'Pagination requires a read returning rows without a range')
  if (!scope || typeof scope.identity !== 'string' || !scope.identity || scope.identity.length > 4096 || typeof scope.schemaFingerprint !== 'string' || !/^[a-f0-9]{64}$/.test(scope.schemaFingerprint)) return fail('invalid_options', 'Use a trusted identity scope and schema fingerprint')
  const params: [string, string][] = []
  url.searchParams.forEach((value, key) => params.push([key, value]))
  params.sort(([a, av], [b, bv]) => a < b ? -1 : a > b ? 1 : av < bv ? -1 : av > bv ? 1 : 0)
  return JSON.stringify([url.origin, url.pathname, params, builder.schema ?? null, headers.get('Accept'), headers.get('Prefer'), scope.identity, scope.schemaFingerprint, order.map(field => [field.column, field.ascending ?? true, field.nullsFirst ?? false, field.unique ?? false]), size])
}
function rowValues(row: Record<string, Scalar>, order: readonly [CursorOrder, ...CursorOrder[]]): Scalar[] {
  try {
    const values = [...cursorFrom(row, order)]
    if (values.some(value => typeof value === 'string' && value.length > 4096)) return fail('invalid_response', 'Ordering values exceed cursor bounds')
    return values
  } catch (error) { if (error instanceof CursorPaginationError) throw error; return fail('invalid_response', 'Select valid scalar ordering columns and a non-null unique tie-breaker') }
}

/** Server-side encrypted continuations over typed schema-derived queries.
 * Keep the key server-only. Scope must reflect verified identity/tenant/permissions.
 * The caller declares the final ordering column unique within the filtered result.
 */
export async function createCursorPager(options: CursorPagerOptions) {
  const ttlMs = options.ttlMs ?? 900000, maxPageSize = options.maxPageSize ?? 999, now = options.now ?? Date.now
  if (!Number.isSafeInteger(ttlMs) || ttlMs < 1 || ttlMs > 86400000 || !Number.isSafeInteger(maxPageSize) || maxPageSize < 1 || maxPageSize > 999 || typeof now !== 'function') return fail('invalid_options', 'Use a TTL up to one day and maximum page size from 1 to 999 (one row is reserved for lookahead)')
  let key: CryptoKey
  if (options.key instanceof Uint8Array) {
    if (options.key.byteLength !== 32) return fail('invalid_options', 'Use a 256-bit cursor key')
    key = await crypto.subtle.importKey('raw', new Uint8Array(options.key), 'AES-GCM', false, ['encrypt', 'decrypt'])
  } else {
    key = options.key
    if (!key || key.type !== 'secret' || key.algorithm.name !== 'AES-GCM' || (key.algorithm as AesKeyAlgorithm).length !== 256 || !key.usages.includes('encrypt') || !key.usages.includes('decrypt')) return fail('invalid_options', 'Use an AES-GCM 256-bit encrypt/decrypt key')
  }
  async function seal(values: Scalar[], binding: string): Promise<string> {
    const issued = timestamp(now), expires = issued + ttlMs
    if (!Number.isSafeInteger(expires)) return fail('invalid_options', 'Invalid cursor expiry')
    const bytes = encoder.encode(JSON.stringify({ v: 1, binding, issued, expires, values } satisfies Payload))
    if (bytes.length > 8192) return fail('invalid_response', 'Ordering values exceed cursor bounds')
    const iv = crypto.getRandomValues(new Uint8Array(12))
    const encrypted = await crypto.subtle.encrypt({ name: 'AES-GCM', iv, additionalData }, key, bytes)
    return `gc1.${base64(iv)}.${base64(new Uint8Array(encrypted))}`
  }
  async function open(token: string, binding: string, order: readonly [CursorOrder, ...CursorOrder[]]): Promise<Scalar[]> {
    let payload: Payload
    try {
      if (typeof token !== 'string' || token.length > 16384) return fail('invalid_cursor', 'Invalid continuation token')
      const parts = token.split('.')
      if (parts.length !== 3 || parts[0] !== 'gc1') return fail('invalid_cursor', 'Invalid continuation token version')
      const iv = unbase64(parts[1]), encrypted = unbase64(parts[2])
      if (iv.length !== 12 || encrypted.length < 16 || encrypted.length > 8208) return fail('invalid_cursor', 'Invalid continuation token')
      const raw = await crypto.subtle.decrypt({ name: 'AES-GCM', iv: new Uint8Array(iv), additionalData }, key, new Uint8Array(encrypted))
      payload = JSON.parse(decoder.decode(raw)) as Payload
      if (!payload || payload.v !== 1 || !Number.isSafeInteger(payload.issued) || !Number.isSafeInteger(payload.expires) || payload.expires <= payload.issued || payload.expires - payload.issued > ttlMs || typeof payload.binding !== 'string' || !Array.isArray(payload.values) || payload.values.length !== order.length) return fail('invalid_cursor', 'Invalid continuation token')
    } catch { return fail('invalid_cursor', 'Invalid or tampered continuation token') }
    const time = timestamp(now)
    if (payload.expires <= time) return fail('expired_cursor', 'Continuation expired; restart pagination')
    if (payload.issued > time || payload.binding !== binding) return fail('incompatible_cursor', 'Continuation does not match this query, identity, ordering, page size or schema')
    const row = Object.fromEntries(order.map((field, i) => [field.column, payload.values[i]])) as Record<string, Scalar>
    try { return [...cursorFrom(row, order)] } catch { return fail('invalid_cursor', 'Invalid continuation values') }
  }
  return {
    async page<Query extends Builder<Query>, const Order extends readonly [CursorOrder, ...CursorOrder[]]>(query: Query & ReadQuery<Query>, request: { order: Order & CheckedOrder<Query, Order> & SelectedOrder<Query, Order>; size: number; scope: CursorScope; after?: string; before?: string }): Promise<CursorPageResult<Query>> {
      if (!Number.isSafeInteger(request.size) || request.size < 1 || request.size > maxPageSize || request.after !== undefined && request.before !== undefined || !Array.isArray(request.order) || request.order.length > 16) return fail('invalid_options', 'Use a bounded page size and either after or before')
      const order = request.order.map(field => ({ ...field })) as unknown as Order
      // Validate ordering without mutating the real query or executing a request.
      const probe = Object.fromEntries(order.map(field => [field.column, 0])) as Record<string, Scalar>
      try { rowValues(probe, order) } catch { return fail('invalid_options', 'Use distinct ordering columns and a final unique tie-breaker') }
      const queryState = queryBinding(query, request.scope, order, request.size)
      const binding = base64(new Uint8Array(await crypto.subtle.digest('SHA-256', encoder.encode(queryState))))
      const direction = request.before === undefined ? 'forward' : 'backward'
      const token = request.before ?? request.after
      const values = token === undefined ? undefined : await open(token, binding, order)
      // Detect mutation of a shared builder during asynchronous cursor validation.
      if (queryBinding(query, request.scope, order, request.size) !== queryState) return fail('invalid_options', 'Query changed during cursor validation')
      const response = await cursorPage(query, { order, size: request.size + 1, ...(values === undefined ? {} : direction === 'forward' ? { after: values } : { before: values }) } as Parameters<typeof cursorPage<Query, Order>>[1]) as Awaited<Query>
      const result = response as { data?: unknown; error?: unknown }
      if (result.error) return { result: response, nextCursor: null, previousCursor: null, hasMore: false, direction }
      if (!Array.isArray(result.data)) return fail('invalid_response', 'Expected a successful array response')
      const hasMore = result.data.length > request.size, rows = result.data.slice(0, request.size)
      let nextCursor: string | null = null, previousCursor: string | null = null
      if (rows.length) {
        const first = rowValues(rows[0] as Record<string, Scalar>, order), last = rowValues(rows.at(-1) as Record<string, Scalar>, order)
        if (direction === 'forward') { if (hasMore) nextCursor = await seal(last, binding); if (token !== undefined) previousCursor = await seal(first, binding) }
        else { if (hasMore) previousCursor = await seal(last, binding); if (token !== undefined) nextCursor = await seal(first, binding) }
      }
      return { result: { ...response, data: rows } as Awaited<Query>, nextCursor, previousCursor, hasMore, direction }
    },
  }
}
