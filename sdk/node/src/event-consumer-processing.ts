// Application-owned processing leases. Tokens and rejected SQL rows are never diagnostics.
import { randomBytes } from 'node:crypto';
import { inspect } from 'node:util';
import { eventConsumerDigests, eventConsumerTransaction, EventConsumerCommitUnknownError, EventConsumerConflictError,
  type EventConsumerPool, type EventConsumerRequest, type EventConsumerTransaction, type EventConsumerTransactionHooks,
  type EventConsumerTransactionOptions, type EventConsumerTransactionResult } from './event-consumer-transactions.js';
export { eventConsumerProcessingSchema } from './event-consumer-processing-contract.js';

export type EventConsumerProcessingErrorCode = 'event_consumer_processing_busy' | 'event_consumer_processing_lease_lost'
  | 'event_consumer_processing_operation_busy' | 'event_consumer_processing_store';
const messages: Record<EventConsumerProcessingErrorCode, string> = {
  event_consumer_processing_busy: 'event consumer processing lease is held',
  event_consumer_processing_lease_lost: 'event consumer processing lease expired or replaced',
  event_consumer_processing_operation_busy: 'event consumer processing lease operation already running',
  event_consumer_processing_store: 'event consumer processing storage operation failed',
};
export class EventConsumerProcessingError extends Error {
  constructor(readonly code: EventConsumerProcessingErrorCode) { super(messages[code]); this.name = 'EventConsumerProcessingError'; }
  toJSON(): object { return { name: this.name, code: this.code }; }
  [inspect.custom](): object { return this.toJSON(); }
}
export const processingStoreError = (): EventConsumerProcessingError => new EventConsumerProcessingError('event_consumer_processing_store');
interface ProcessingState { input: EventConsumerRequest; identity: Buffer; request: Buffer; token: Buffer; completed: boolean; busy: boolean }
const states = new WeakMap<EventConsumerProcessingLease, ProcessingState>(), constructorKey = Symbol();
function same(value: unknown, expected: Buffer): boolean { return value instanceof Uint8Array && expected.equals(Buffer.from(value)); }
function duration(value: number): void {
  // Maximum positive whole milliseconds representable by Go time.Duration.
  if (!Number.isSafeInteger(value) || value < 1 || value > 9223372036854) throw new TypeError('invalid consumer processing lease duration');
}
function signalOption(options: EventConsumerTransactionOptions): AbortSignal | undefined {
  if (!options || typeof options !== 'object' || Array.isArray(options)
    || options.signal !== undefined && !(options.signal instanceof AbortSignal)) throw new TypeError('invalid consumer processing signal');
  const signal = options.signal; signal?.throwIfAborted(); return signal;
}
function poolOption(pool: EventConsumerPool): void {
  if (!pool || typeof pool.connect !== 'function') throw new TypeError('invalid event consumer pool');
}
async function transaction<T>(pool: EventConsumerPool, signal: AbortSignal | undefined,
  operation: (tx: EventConsumerTransaction) => Promise<T>): Promise<T> {
  poolOption(pool); signal?.throwIfAborted();
  const connection = await pool.connect().catch(() => { throw processingStoreError(); });
  const query: EventConsumerTransaction['query'] = async (sql, values) => {
    signal?.throwIfAborted();
    const result = await connection.query(sql, values).catch(() => { throw processingStoreError(); });
    signal?.throwIfAborted(); return result;
  };
  let discard = false;
  try {
    await query('BEGIN ISOLATION LEVEL READ COMMITTED');
    const result = await operation(Object.freeze({ query }));
    signal?.throwIfAborted();
    try { await connection.query('COMMIT'); }
    catch { discard = true; throw new EventConsumerCommitUnknownError(processingStoreError()); }
    return result;
  } catch (error) {
    try { await connection.query('ROLLBACK'); } catch { discard = true; }
    throw error;
  } finally { connection.release(discard); }
}
function affected(rows: Record<string, unknown>[], identity: Buffer): void {
  if (rows.length === 0) throw new EventConsumerProcessingError('event_consumer_processing_lease_lost');
  if (rows.length !== 1 || !same(rows[0]!.identity_digest, identity)) throw processingStoreError();
}
async function claim(tx: EventConsumerTransaction, state: ProcessingState, durationMs: number): Promise<boolean> {
  const { input, identity, request, token } = state;
  await tx.query('SELECT pg_advisory_xact_lock(hashtextextended($1, 0))', ['gregale.event-consumer-inbox.v1:' + identity.toString('hex')]);
  const receipt = (await tx.query(`SELECT account_id=$2::uuid AND app_id=$3::uuid AND subscription_id=$4::uuid AND acceptance_id=$5::bigint AND event_source=$6 AND event_id=$7 AND request_digest=$8 AS matching FROM public.gregale_event_consumer_inbox WHERE identity_digest=$1`,
    [identity, input.accountId, input.appId, input.subscriptionId, input.acceptanceId, input.source, input.eventId, request])).rows[0];
  if (receipt) {
    if (typeof receipt.matching !== 'boolean') throw processingStoreError();
    if (!receipt.matching) throw new EventConsumerConflictError();
    return true;
  }
  await tx.query(`INSERT INTO public.gregale_event_consumer_processing (identity_digest,request_digest,claim_token,expires_at) VALUES ($1,$2,$3,clock_timestamp()+$4::bigint*interval '1 millisecond') ON CONFLICT (identity_digest) DO NOTHING`, [identity, request, token, durationMs]);
  const row = (await tx.query(`SELECT request_digest,claim_token,expires_at<=clock_timestamp() AS expired FROM public.gregale_event_consumer_processing WHERE identity_digest=$1 FOR UPDATE`, [identity])).rows[0];
  if (!row || typeof row.expired !== 'boolean' || !(row.claim_token instanceof Uint8Array) || row.claim_token.byteLength !== 32) throw processingStoreError();
  if (!same(row.request_digest, request)) throw new EventConsumerConflictError();
  if (!same(row.claim_token, token)) {
    if (!row.expired) throw new EventConsumerProcessingError('event_consumer_processing_busy');
    affected((await tx.query(`UPDATE public.gregale_event_consumer_processing SET claim_token=$2,expires_at=clock_timestamp()+$3::bigint*interval '1 millisecond' WHERE identity_digest=$1 RETURNING identity_digest`, [identity, token, durationMs])).rows, identity);
  }
  return false;
}
/** A process-local capability. Never persist or format its token; copies do not create a new claim. */
export class EventConsumerProcessingLease {
  private constructor(key: symbol, state: ProcessingState) {
    if (key !== constructorKey) throw new TypeError('use claimEventConsumerProcessing');
    states.set(this, state); Object.freeze(this);
  }
  /** True only when claim observed an existing receipt. Complete still validates/replays it. */
  get completed(): boolean { return states.get(this)!.completed; }
  toJSON(): string { return this.toString(); }
  toString(): string { return 'EventConsumerProcessingLease{redacted}'; }
  [inspect.custom](): string { return this.toString(); }
  static async claim(pool: EventConsumerPool, input: EventConsumerRequest, durationMs: number,
    options: EventConsumerTransactionOptions = {}): Promise<EventConsumerProcessingLease> {
    const signal = signalOption(options); duration(durationMs); poolOption(pool);
    const { identity, request } = eventConsumerDigests(input);
    const state: ProcessingState = { input, identity, request, token: randomBytes(32), completed: false, busy: false };
    state.completed = await transaction(pool, signal, tx => claim(tx, state, durationMs));
    // An uncertain COMMIT returns no capability, including the already-completed path.
    return new EventConsumerProcessingLease(constructorKey, state);
  }
  /** Extends only an unexpired matching claim. Serialize with completion and stop work on failure. */
  async renew(pool: EventConsumerPool, durationMs: number, options: EventConsumerTransactionOptions = {}): Promise<void> {
    const signal = signalOption(options); duration(durationMs); poolOption(pool);
    const state = states.get(this);
    if (!state) throw new TypeError('invalid consumer processing lease');
    return withEventConsumerProcessingLease(this, state.input, async () => transaction(pool, signal, async tx => {
      affected((await tx.query(`UPDATE public.gregale_event_consumer_processing SET expires_at=clock_timestamp()+$4::bigint*interval '1 millisecond' WHERE identity_digest=$1 AND request_digest=$2 AND claim_token=$3 AND expires_at>clock_timestamp() RETURNING identity_digest`,
        [state.identity, state.request, state.token, durationMs])).rows, state.identity);
    }));
  }
  /** Short transactional writes only. A committed receipt replays without checking an old lease. */
  async complete(pool: EventConsumerPool, handler: (tx: EventConsumerTransaction) => Promise<string>,
    options: EventConsumerTransactionOptions = {}): Promise<EventConsumerTransactionResult> {
    const signal = signalOption(options); poolOption(pool);
    if (typeof handler !== 'function') throw new TypeError('invalid event consumer handler');
    const state = states.get(this);
    if (!state) throw new TypeError('invalid consumer processing lease');
    return withEventConsumerProcessingLease(this, state.input, hooks => eventConsumerTransaction(pool, state.input, handler,
      { signal }, { ...hooks, storageError: processingStoreError }));
  }
}
/** Internal composition holds the lease's operation guard through worker completion. */
export async function withEventConsumerProcessingLease<T>(lease: EventConsumerProcessingLease, input: EventConsumerRequest,
  operation: (hooks: EventConsumerTransactionHooks) => Promise<T>): Promise<T> {
  const state = states.get(lease);
  if (!state) throw new TypeError('invalid consumer processing lease');
  const { identity, request } = eventConsumerDigests(input);
  if (!same(state.identity, identity) || !same(state.request, request)) throw new TypeError('consumer processing lease input differs');
  if (state.busy) throw new EventConsumerProcessingError('event_consumer_processing_operation_busy');
  state.busy = true;
  const values = [state.identity, state.request, state.token];
  try {
    return await operation({
      beforeHandler: async tx => {
        const row = (await tx.query(`SELECT request_digest=$2 AND claim_token=$3 AND expires_at>clock_timestamp() AS valid FROM public.gregale_event_consumer_processing WHERE identity_digest=$1 FOR UPDATE`, values)).rows[0];
        if (row && typeof row.valid !== 'boolean') throw processingStoreError();
        if (!row || !row.valid) throw new EventConsumerProcessingError('event_consumer_processing_lease_lost');
      },
      afterReceipt: async tx => affected((await tx.query(`DELETE FROM public.gregale_event_consumer_processing WHERE identity_digest=$1 AND request_digest=$2 AND claim_token=$3 AND expires_at>clock_timestamp() RETURNING identity_digest`, values)).rows, state.identity),
    });
  } finally { state.busy = false; }
}
export const claimEventConsumerProcessing = (pool: EventConsumerPool, input: EventConsumerRequest, durationMs: number,
  options: EventConsumerTransactionOptions = {}): Promise<EventConsumerProcessingLease> => EventConsumerProcessingLease.claim(pool, input, durationMs, options);
