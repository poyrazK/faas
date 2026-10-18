import { isReplayExpired } from './replay-key.js'

type ReceiptResponse<Row, Error> = { data: Row[] | null; error: Error | null; status: number }

export type MutationReconciliation<Row, Error> =
  | { outcome: 'confirmed'; row: Row; status: number }
  | { outcome: 'payload_conflict'; error: Error; status: number }
  | { outcome: 'unresolved'; reason: 'not_found' | 'request_failed' | 'invalid_receipt' | 'replay_expired'; error: Error | null; status: number }

/** Classify a completed read-only receipt lookup. This helper executes no
 * requests, polling, or retries. matches must synchronously bind the returned
 * receipt to the pending operation's API/identity/key/payload contract. Empty
 * results never prove rollback: a write may be in flight or its receipt purged.
 * Keep the original operation until the application explicitly resolves it.
 */
export function reconcileMutation<Row, Error, Matched extends Row>(
  result: ReceiptResponse<Row, Error>,
  matches: (row: Row) => row is Matched,
): MutationReconciliation<Matched, Error>
export function reconcileMutation<Row, Error>(
  result: ReceiptResponse<Row, Error>,
  matches: (row: Row) => boolean,
): MutationReconciliation<Row, Error>
export function reconcileMutation<Row, Error>(
  result: ReceiptResponse<Row, Error>,
  matches: (row: Row) => boolean,
): MutationReconciliation<Row, Error> {
  const { data, error, status } = result
  if (error !== null || !(status >= 200 && status < 300)) {
    if (status === 410 && isReplayExpired(error)) return { outcome: 'unresolved', reason: 'replay_expired', error, status }
    if (status === 409 && error !== null && typeof error === 'object' &&
        'code' in error && error.code === 'PT409' &&
        'message' in error && error.message === 'idempotency_payload_conflict') {
      return { outcome: 'payload_conflict', error, status }
    }
    return { outcome: 'unresolved', reason: 'request_failed', error, status }
  }
  if (Array.isArray(data) && data.length === 0) return { outcome: 'unresolved', reason: 'not_found', error: null, status }
  if (Array.isArray(data) && data.length === 1 && data[0] !== null && typeof data[0] === 'object' && !Array.isArray(data[0])) {
    try {
      const matched: unknown = matches(data[0])
      if (matched !== null && (typeof matched === 'object' || typeof matched === 'function') && typeof (matched as { then?: unknown }).then === 'function') {
        void Promise.resolve(matched).catch(() => {})
      }
      if (matched === true) return { outcome: 'confirmed', row: data[0], status }
    } catch { /* A predicate can include private context; omit its exception. */ }
  }
  return { outcome: 'unresolved', reason: 'invalid_receipt', error: null, status }
}
