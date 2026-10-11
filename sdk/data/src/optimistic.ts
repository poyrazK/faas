type MutationQuery<Row, Error> = PromiseLike<{ data: Row[] | null; error: Error | null; status: number }> & { retry(enabled: boolean): unknown }
export type OptimisticResult<Row, Error> =
  | { outcome: 'applied'; row: Row }
  | { outcome: 'conflict' }
  | { outcome: 'error'; error: Error; status: number }

/** Execute a single-row conditional mutation with select() and no retries.
 * Filter by the complete primary key and the previously read version. The
 * database must advance versions atomically. A missing or RLS-hidden row has
 * the same conflict outcome as a stale version; no existence information leaks.
 */
export async function optimisticMutation<Row, Error>(query: MutationQuery<Row, Error> & (unknown extends Row ? never : unknown)): Promise<OptimisticResult<Row, Error>> {
  query.retry(false)
  const result = await query
  if (result.error !== null) return { outcome: 'error', error: result.error, status: result.status }
  if (!Array.isArray(result.data)) throw new Error('Optimistic mutations require select() returning rows')
  if (result.data.length === 0) return { outcome: 'conflict' }
  if (result.data.length !== 1) throw new Error('Optimistic mutations must target one primary key')
  return { outcome: 'applied', row: result.data[0] }
}
