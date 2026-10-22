import { PostgrestClient } from '@supabase/postgrest-js'

import { authenticatedFetch, dataAPIBase, type DataClientOptions } from './client-fetch.js'

export type { DataClientOptions, DataResponseInfo } from './client-fetch.js'

/** Application tokens authenticate data access; account-management keys do not. */
export function createDataClient<Database>(options: DataClientOptions): PostgrestClient<Database> {
  const send = authenticatedFetch(options)
  const restEndpoint = `${dataAPIBase(options.url)}/rest/v1`
  const expectedFingerprint = options.expectedSchemaFingerprint
  if (expectedFingerprint !== undefined && (typeof expectedFingerprint !== 'string' || !/^[a-f0-9]{64}$/.test(expectedFingerprint))) throw new Error('Expected schema fingerprint must be 64 lowercase hexadecimal characters')
  // A distinct route fails closed on runtimes that predate the guard. A header
  // alone could be ignored by an older gateway before executing a mutation.
  const endpoint = expectedFingerprint === undefined ? restEndpoint : `${restEndpoint.slice(0, -'/rest/v1'.length)}/__gregale/contract/${expectedFingerprint}/rest/v1`
  // The generated contract retains the PostgREST schema shape and supports
  // .schema('api'), typed inserts, filters, relationships and projections.
  return new PostgrestClient<Database>(endpoint, { fetch: send })
}

export { PostgrestError } from '@supabase/postgrest-js'
export { createStorageClient, StorageError } from './storage.js'
export type { StorageBucket, StorageClient, StorageObject, StorageUploadBody } from './storage.js'
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
