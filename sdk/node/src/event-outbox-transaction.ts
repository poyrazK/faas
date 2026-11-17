/** Own bounded PostgreSQL transactions; never retry business callbacks. */
import { jobScope, JobError } from './job-runtime.js';
import type { OperationConnection, OperationPool, OperationTransaction } from './operation-receipt.js';
import { EventOutboxError, type EventOutboxOptions } from './event-outbox-state.js';
export type EventOutboxPool = OperationPool;
export interface EventOutboxTransaction extends OperationTransaction { readonly outboxTransaction: true; }
const live = new WeakMap<EventOutboxTransaction, { fail: (error: EventOutboxError) => void }>();
export function eventOutboxTransactionQuery(tx: EventOutboxTransaction): OperationTransaction['query'] {
  if (!live.has(tx)) throw new EventOutboxError('invalid_transaction'); return tx.query;
}
export function failEventOutboxTransaction(tx: EventOutboxTransaction, error: EventOutboxError): void { live.get(tx)?.fail(error); }
export async function withEventOutboxTransaction<T>(pool: EventOutboxPool, handler: (tx: EventOutboxTransaction) => Promise<T>, options: EventOutboxOptions): Promise<T> {
  if (!Number.isSafeInteger(options.timeoutMs) || options.timeoutMs <= 0 || options.timeoutMs > 2147483647) throw new EventOutboxError('invalid_request');
  let connection: OperationConnection | undefined, released = false, commitSent = false, committed = false;
  let tx: EventOutboxTransaction | undefined;
  let failure: EventOutboxError | undefined;
  const release = (discard: boolean) => { if (connection && !released) { released = true; try { connection.release(discard); } catch { /* Driver cleanup cannot expose private diagnostics. */ } } };
  try {
    return await jobScope(options.timeoutMs, options.signal, async signal => {
      try {
        connection = await pool.connect(); signal.throwIfAborted();
        const query: OperationTransaction['query'] = async (sql, values = []) => {
          signal.throwIfAborted();
          try { const result = await connection!.query(sql, values); signal.throwIfAborted(); return result; }
          catch { signal.throwIfAborted(); failure ??= new EventOutboxError('store_failed'); throw failure; }
        };
        await query('BEGIN ISOLATION LEVEL READ COMMITTED');
        await query("SELECT set_config('statement_timeout',$1,true),set_config('idle_in_transaction_session_timeout',$1,true)", [String(options.timeoutMs)]);
        tx = Object.freeze({ outboxTransaction: true as const, query: (sql: string, values?: unknown[]) => {
          if (!live.has(tx!)) throw new EventOutboxError('invalid_transaction'); return query(sql, values);
        } });
        live.set(tx, { fail: error => { failure ??= error; } });
        const value = await handler(tx); signal.throwIfAborted(); live.delete(tx);
        if (failure) throw failure;
        commitSent = true;
        try { await connection.query('COMMIT'); committed = true; }
        catch { throw new EventOutboxError('commit_unknown'); }
        return value;
      } finally {
        if (tx) live.delete(tx);
        if (connection && !released) {
          if (!committed) try { await connection.query('ROLLBACK'); } catch { release(true); }
          release(!committed);
        }
      }
    });
  } catch (error) {
    if (tx) live.delete(tx); release(true);
    if (commitSent && !committed) throw new EventOutboxError('commit_unknown');
    if (error instanceof EventOutboxError) throw error;
    if (error instanceof JobError && error.code === 'deadline') throw new EventOutboxError('deadline');
    if (options.signal?.aborted) throw new EventOutboxError('cancelled');
    throw new EventOutboxError('store_failed');
  }
}
