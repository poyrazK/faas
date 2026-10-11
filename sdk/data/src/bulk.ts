import { dataRequest } from './deadline.js'

type BulkQuery<Row, Error> = PromiseLike<{ data: Row[] | null; error: Error | null; status: number }> & {
  abortSignal(signal: AbortSignal): unknown
  retry(enabled: boolean): unknown
}
export type BulkOutcome<Row, Error> =
  | { index: number; outcome: 'applied'; rows: Row[] }
  | { index: number; outcome: 'conflict' }
  | { index: number; outcome: 'error'; error: Error; status: number }
  | { index: number; outcome: 'uncertain'; error: unknown; status: number }
  | { index: number; outcome: 'skipped'; reason: 'cancelled' }
  | { index: number; outcome: 'invalid'; error: unknown }
export type BulkProgress = Readonly<{ completed: number; total: number; applied: number; conflicts: number; errors: number; uncertain: number; skipped: number; invalid: number }>
export type BulkOptions = {
  /** Items in a scheduling window; each item remains a separate transaction. */
  batchSize?: number
  concurrency?: number
  timeoutMs?: number
  signal?: AbortSignal
  /** Synchronous observer; exceptions cannot interrupt writes. No row data is sent. */
  onProgress?: (progress: BulkProgress) => void
}

/** Execute fresh selected mutations once, preserving input order in the report.
 * Windows and the overall run are not atomic. Reconcile uncertain writes before
 * retrying; use idempotent RPCs or primary-key/version predicates where needed.
 */
export async function bulkMutations<Item, Row, Error extends { code: string }>(
  items: readonly Item[],
  build: (item: Item, index: number) => BulkQuery<Row, Error> & (unknown extends Row ? never : unknown),
  options: BulkOptions = {},
): Promise<BulkOutcome<Row, Error>[]> {
  const { batchSize = 100, concurrency = 2, timeoutMs = 5000, signal } = options
  for (const [name, value] of [['batchSize', batchSize], ['concurrency', concurrency]] as const) {
    if (!Number.isSafeInteger(value) || value < 1) throw new Error(`${name} must be a positive safe integer`)
  }
  if (!Number.isSafeInteger(timeoutMs) || timeoutMs < 1 || timeoutMs > 2147483647) throw new Error('Invalid bulk request timeout')
  const input = [...items]
  const results: BulkOutcome<Row, Error>[] = new Array(input.length)
  const counts = { completed: 0, total: input.length, applied: 0, conflicts: 0, errors: 0, uncertain: 0, skipped: 0, invalid: 0 }
  const record = (result: BulkOutcome<Row, Error>) => {
    results[result.index] = result
    counts.completed++
    const field = result.outcome === 'conflict' ? 'conflicts' : result.outcome === 'error' ? 'errors' : result.outcome
    counts[field]++
    try { void Promise.resolve(options.onProgress?.(Object.freeze({ ...counts }))).catch(() => {}) } catch { /* Observers cannot interrupt mutations. */ }
  }
  for (let start = 0; start < input.length; start += batchSize) {
    const end = Math.min(input.length, start + batchSize)
    let next = start
    await Promise.all(Array.from({ length: Math.min(concurrency, end - start) }, async () => {
      while (next < end) {
        const index = next++
        if (signal?.aborted) { record({ index, outcome: 'skipped', reason: 'cancelled' }); continue }
        let query: BulkQuery<Row, Error>
        try { query = build(input[index], index) }
        catch (error) { record({ index, outcome: 'invalid', error }); continue }
        try {
          const result = await dataRequest(query, { timeoutMs, signal })
          if (result.error) record({ index, outcome: result.status === 0 || result.status >= 500 ? 'uncertain' : 'error', error: result.error, status: result.status })
          else if (!Array.isArray(result.data)) record({ index, outcome: 'uncertain', error: new Error('Bulk mutations require select() returning rows'), status: result.status })
          else if (!result.data.length) record({ index, outcome: 'conflict' })
          else record({ index, outcome: 'applied', rows: result.data })
        } catch (error) { record({ index, outcome: 'uncertain', error, status: 0 }) }
      }
    }))
  }
  return results
}
