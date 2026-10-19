import { reconcileMutation, type MutationReconciliation } from './reconcile.js'

export type VersionedMutationResult<Row, Error> =
  | { outcome: 'applied'; row: Row; status: number }
  | { outcome: 'version_conflict'; error: Error; status: number }
  | Exclude<MutationReconciliation<Row, Error>, { outcome: 'confirmed' }>

/** Classify a completed v2 conditional-write response, without executing or
 * retrying a request. matches must bind its selected snapshot to the original
 * identity, target and expected result. Missing rows and expired keys remain
 * unresolved; neither permits replacing the original pending operation.
 * Use reconcileMutation for read-only receipt lookups.
 */
export function classifyVersionedMutation<Row, Error, Matched extends Row>(
  response: { data: Row[] | null; error: Error | null; status: number },
  matches: (row: Row) => row is Matched,
): VersionedMutationResult<Matched, Error>
export function classifyVersionedMutation<Row, Error>(
  response: { data: Row[] | null; error: Error | null; status: number },
  matches: (row: Row) => boolean,
): VersionedMutationResult<Row, Error>
export function classifyVersionedMutation<Row, Error>(
  response: { data: Row[] | null; error: Error | null; status: number },
  matches: (row: Row) => boolean,
): VersionedMutationResult<Row, Error> {
  const { error, status } = response
  if (status === 409 && error !== null && typeof error === 'object' &&
      'code' in error && error.code === 'PT409' &&
      'message' in error && error.message === 'idempotency_version_conflict') {
    return { outcome: 'version_conflict', error, status }
  }
  const result = reconcileMutation(response, matches)
  return result.outcome === 'confirmed'
    ? { outcome: 'applied', row: result.row, status: result.status }
    : result
}
