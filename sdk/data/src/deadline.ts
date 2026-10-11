import type { QueryResult } from './index.js'

type RequestQuery = PromiseLike<{ status: number; error: { code: string } | null }> & {
  abortSignal(signal: AbortSignal): unknown
  retry(enabled: boolean): unknown
}

/** Execute once with a deadline covering token lookup, transport and body reads.
 * Cancelling a mutation does not prove it was rolled back. Reconcile its outcome
 * before retrying. This helper disables automatic retries.
 */
export async function dataRequest<Query extends RequestQuery>(query: Query, options: { timeoutMs: number; signal?: AbortSignal }): Promise<QueryResult<Query>> {
  if (!Number.isSafeInteger(options.timeoutMs) || options.timeoutMs < 1 || options.timeoutMs > 2147483647) throw new Error('Request timeout must be a positive integer at most 2147483647 ms')
  const controller = new AbortController()
  const cancel = () => controller.abort(new DOMException('Request cancelled', 'AbortError'))
  if (options.signal?.aborted) cancel()
  else options.signal?.addEventListener('abort', cancel, { once: true })
  const timer = setTimeout(() => controller.abort(new DOMException('Request deadline exceeded', 'TimeoutError')), options.timeoutMs)
  try {
    query.abortSignal(controller.signal)
    query.retry(false)
    const result = await query
    if (result.status === 0 && controller.signal.aborted && result.error) {
      const timeout = controller.signal.reason?.name === 'TimeoutError'
      return { ...result, error: { ...result.error, code: timeout ? 'request_timeout' : 'request_cancelled' } } as QueryResult<Query>
    }
    return result as QueryResult<Query>
  } finally {
    clearTimeout(timer)
    options.signal?.removeEventListener('abort', cancel)
  }
}
