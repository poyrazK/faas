import { EventConsumerCommitUnknownError, type EventConsumerPool, type EventConsumerTransaction } from './event-consumer-transactions.js';
import { intentPool, intentStoreError } from './event-acknowledgment-intent-store.js';

async function storage<T>(operation: () => Promise<T>): Promise<T> {
  try { return await operation(); } catch { throw intentStoreError(); }
}
/** Dedicated connection, joined cleanup and no COMMIT for read/replay. No external callbacks. */
export async function intentCommandTransaction<T>(pool: EventConsumerPool, signal: AbortSignal | undefined,
  operation: (tx: EventConsumerTransaction) => Promise<{ value: T; write: boolean }>): Promise<T> {
  intentPool(pool); signal?.throwIfAborted();
  const connection = await storage(() => pool.connect());
  let discard = false, failed = false, failure: unknown, result: T | undefined;
  const query: EventConsumerTransaction['query'] = async (sql, values) => {
    signal?.throwIfAborted();
    const response = await storage(() => connection.query(sql, values));
    signal?.throwIfAborted(); return response;
  };
  try {
    await query('BEGIN ISOLATION LEVEL READ COMMITTED');
    const outcome = await operation(Object.freeze({ query }));
    signal?.throwIfAborted();
    if (outcome.write) {
      try { await connection.query('COMMIT'); }
      catch {
        discard = true;
        const error = new EventConsumerCommitUnknownError(intentStoreError());
        error.message = 'acknowledgment intent command commit outcome unknown; retry the original command';
        throw error;
      }
    } else await query('ROLLBACK');
    result = outcome.value;
  } catch (error) {
    failed = true; failure = error;
    try { await connection.query('ROLLBACK'); } catch { discard = true; }
  } finally {
    try { connection.release(discard); }
    catch { if (!failed) { failed = true; failure = intentStoreError(); } }
  }
  if (failed) throw failure;
  return result!;
}
