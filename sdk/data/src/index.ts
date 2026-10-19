import { PostgrestClient } from '@supabase/postgrest-js'

export type DataResponseInfo = Readonly<{
  requestId: string | null
  status: number
  /** Client time from token lookup to response headers; excludes body consumption. */
  durationMs: number
}>

type DataClientBaseOptions = {
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

async function resolveToken<Token>(value: Token | Promise<Token>, signal?: AbortSignal | null): Promise<Token> {
  if (!signal) return value
  signal.throwIfAborted()
  return new Promise((resolve, reject) => {
    const aborted = () => { signal.removeEventListener('abort', aborted); reject(signal.reason) }
    signal.addEventListener('abort', aborted, { once: true })
    Promise.resolve(value).then(resolve, reject).finally(() => signal.removeEventListener('abort', aborted))
  })
}

/** Application tokens authenticate data access; account-management keys do not. */
export function createDataClient<Database>(options: DataClientOptions): PostgrestClient<Database> {
  if (options.anonymous !== undefined && typeof options.anonymous !== 'boolean') throw new Error('anonymous must be a boolean')
  const anonymous = options.anonymous === true
  const url = new URL(options.url)
  if (url.username || url.password || url.search || url.hash) throw new Error('Data API URL must not contain credentials, a query or a fragment')
  if (url.protocol !== 'https:' && !(url.protocol === 'http:' && ['localhost', '127.0.0.1', '[::1]'].includes(url.hostname))) throw new Error('Data API requires HTTPS')
  const base = url.toString().replace(/\/$/, '')
  const restEndpoint = base.endsWith('/rest/v1') ? base : `${base}/rest/v1`
  const expectedFingerprint = options.expectedSchemaFingerprint
  if (expectedFingerprint !== undefined && (typeof expectedFingerprint !== 'string' || !/^[a-f0-9]{64}$/.test(expectedFingerprint))) throw new Error('Expected schema fingerprint must be 64 lowercase hexadecimal characters')
  // A distinct route fails closed on runtimes that predate the guard. A header
  // alone could be ignored by an older gateway before executing a mutation.
  const endpoint = expectedFingerprint === undefined ? restEndpoint : `${restEndpoint.slice(0, -'/rest/v1'.length)}/__gregale/contract/${expectedFingerprint}/rest/v1`
  const fetcher = options.fetch ?? globalThis.fetch
  const authenticatedFetch: typeof globalThis.fetch = async (input, init) => {
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
      const id = response.headers.get('X-Request-Id')
      const info: DataResponseInfo = Object.freeze({
        requestId: id?.length === 36 && requestIdPattern.test(id) ? id : null,
        status: response.status,
        durationMs: performance.now() - started,
      })
      try { void Promise.resolve(options.onResponse(info)).catch(() => {}) } catch { /* Diagnostics must not affect data access. */ }
    }
    return response
  }
  // The generated contract retains the PostgREST schema shape and supports
  // .schema('api'), typed inserts, filters, relationships and projections.
  return new PostgrestClient<Database>(endpoint, { fetch: authenticatedFetch })
}

export { PostgrestError } from '@supabase/postgrest-js'
export { classifyDataError } from './errors.js'
export type { ClassifiedDataError, DataErrorCategory, DataErrorRecovery } from './errors.js'
export { validatedRpc, RpcValidationError } from './rpc.js'
export type { ValidatedRpcResult } from './rpc.js'
export { validateRpcResponse, RpcResponseValidationError } from './response.js'
export type { ValidatedRpcResponse } from './response.js'
export { jsonText, jsonValue, selectJson } from './json.js'
export type { Json, JsonPath, JsonProjection } from './json.js'
export { validateJson, JsonValidationError } from './validation.js'
export type { JsonParser, ValidatedJson } from './validation.js'

/** Preserve the exact projection and nested relationship types of a query. */
export type QueryResult<Query> = Query extends PromiseLike<infer Result> ? Result : never
export type QueryData<Query> = QueryResult<Query> extends { data: infer Data } ? NonNullable<Data> : never
export type QueryError<Query> = QueryResult<Query> extends { error: infer Error } ? NonNullable<Error> : never
export type { PostgrestResponse, PostgrestSingleResponse, PostgrestMaybeSingleResponse } from '@supabase/postgrest-js'
export { cursorPage, cursorFrom } from './cursor.js'
export type { Cursor, CursorOrder } from './cursor.js'
export { dataRequest } from './deadline.js'
export { optimisticMutation } from './optimistic.js'
export type { OptimisticResult } from './optimistic.js'
export { bulkMutations } from './bulk.js'
export type { BulkOutcome, BulkProgress, BulkOptions } from './bulk.js'
export { reconcileMutation } from './reconcile.js'
export type { MutationReconciliation } from './reconcile.js'
export { createReplayKeyV2 } from './replay-key.js'
export { classifyVersionedMutation } from './versioned-mutation.js'
export type { VersionedMutationResult } from './versioned-mutation.js'

export { createCursorPager, CursorPaginationError } from './cursor-token.js'
export type { CursorScope, CursorPagerOptions, CursorPageResult } from './cursor-token.js'

export { createMutationJournal, MutationJournalError, MUTATION_JOURNAL_LIMITS } from './mutation-journal.js'
export type { MutationJournalContext, JournalOperation, MutationJournalState, MutationJournalEntry, MutationJournalStore, MutationJournalErrorCode, JournalReconciliation } from './mutation-journal.js'
export { createIndexedDBMutationStore } from './indexeddb-mutation-store.js'
