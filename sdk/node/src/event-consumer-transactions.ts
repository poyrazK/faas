/** Application-owned transactional event receipts. Authenticate delivery first. */
import { createHash } from 'node:crypto';
import type { OperationPool, OperationTransaction } from './operation-receipt.js';
import { eventConsumerRequestMaxBytes, eventConsumerResultMaxBytes, eventConsumerIdentityMaxBytes } from './event-consumer-contract.js';
export { eventConsumerReceiptSchema } from './event-consumer-contract.js';
export type EventConsumerPool = OperationPool;
export type EventConsumerTransaction = OperationTransaction;

export interface EventConsumerIdentity {
  readonly accountId: string;
  readonly appId: string;
  readonly subscriptionId: string;
  /** Canonical positive decimal int64; a string preserves IDs above 2^53. */
  readonly acceptanceId: string;
  readonly source: string;
  readonly eventId: string;
}
export interface EventConsumerRequest extends EventConsumerIdentity {}
export interface EventConsumerTransactionResult { body: string; replayed: boolean }
export interface EventConsumerTransactionOptions {
  /** Cooperative cancellation between queries and before COMMIT. Configure driver timeouts for active queries. */
  signal?: AbortSignal;
}
const requests = new WeakMap<EventConsumerRequest, Buffer>();
export class EventConsumerConflictError extends Error {
  readonly code = 'event_consumer_receipt_conflict';
  constructor() { super('event consumer receipt scope or content differs'); this.name = 'EventConsumerConflictError'; }
}
export class EventConsumerCommitUnknownError extends Error {
  readonly code = 'event_consumer_commit_unknown';
  constructor(cause: unknown) {
    super('event consumer commit outcome unknown; retry the same acceptance and consumer', { cause });
    this.name = 'EventConsumerCommitUnknownError';
  }
}
export function eventConsumerUUID(value: string): string {
  if (typeof value !== 'string' || !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(value)
      || value === '00000000-0000-0000-0000-000000000000') throw new TypeError('event consumer scope must be nonzero UUIDs');
  return value.toLowerCase();
}
function utf8(body: Uint8Array): string { return new TextDecoder('utf-8', { fatal: true, ignoreBOM: true }).decode(body); }
/** Supply authenticated identity and the exact original CloudEvents bytes. */
export function newEventConsumerRequest(identity: EventConsumerIdentity, body: Uint8Array): EventConsumerRequest {
  identity = { ...identity };
  if (!(body instanceof Uint8Array) || body.byteLength === 0 || body.byteLength > eventConsumerRequestMaxBytes
      || typeof identity.acceptanceId !== 'string' || !/^[1-9][0-9]{0,18}$/.test(identity.acceptanceId)
      || BigInt(identity.acceptanceId) > 9223372036854775807n) throw new TypeError('invalid event consumer request');
  for (const value of [identity.source, identity.eventId]) {
    if (typeof value !== 'string' || !value || Buffer.byteLength(value) > eventConsumerIdentityMaxBytes
        || /[\r\n\0]/.test(value) || Buffer.from(value).toString('utf8') !== value) throw new TypeError('invalid event identity');
  }
  const copy = Buffer.from(body);
  const envelope: unknown = JSON.parse(utf8(copy));
  if (!envelope || typeof envelope !== 'object' || Array.isArray(envelope)) throw new TypeError('invalid event envelope');
  const event = envelope as Record<string, unknown>;
  if (event.specversion !== '1.0' || event.source !== identity.source || event.id !== identity.eventId
      || typeof event.type !== 'string' || !event.type || !Object.hasOwn(event, 'data')) throw new TypeError('event envelope identity differs');
  const request = Object.freeze({ accountId: eventConsumerUUID(identity.accountId), appId: eventConsumerUUID(identity.appId),
    subscriptionId: eventConsumerUUID(identity.subscriptionId), acceptanceId: identity.acceptanceId, source: identity.source, eventId: identity.eventId });
  requests.set(request, copy);
  return request;
}
/** Headers are not authentication. Supply owner-configured scope and authenticate the route first. */
export function eventConsumerRequestFromHeaders(
  headers: Record<string, string | readonly string[] | undefined>, body: Uint8Array, accountId: string, appId: string,
): EventConsumerRequest {
  const read = (name: string): string => {
    const values = Object.entries(headers).filter(([key]) => key.toLowerCase() === name)
      .flatMap(([, value]) => value === undefined ? [] : typeof value === 'string' ? [value] : [...value]);
    if (values.length !== 1 || !values[0]) throw new TypeError('missing or duplicate event context');
    return values[0];
  };
  return newEventConsumerRequest({ accountId, appId, subscriptionId: read('x-gregale-event-subscription-id'),
    acceptanceId: read('x-gregale-event-acceptance-id'), source: read('x-gregale-event-source'), eventId: read('x-gregale-event-id') }, body);
}
function validateResult(body: string): void {
  if (typeof body !== 'string' || !body || Buffer.byteLength(body) > eventConsumerResultMaxBytes
      || body.includes('\0') || Buffer.from(body).toString('utf8') !== body) throw new TypeError('invalid event consumer result');
  JSON.parse(body);
}
/** Own a dedicated pg connection and READ COMMITTED transaction. Callback uses
 * only supplied queries, returns exact JSON text, and must not control the
 * transaction or make external effects. Acknowledge only after success. */
export async function withEventConsumerTransaction(
  pool: EventConsumerPool, input: EventConsumerRequest, handler: (transaction: EventConsumerTransaction) => Promise<string>,
  options: EventConsumerTransactionOptions = {},
): Promise<EventConsumerTransactionResult> {
  return eventConsumerTransaction(pool, input, handler, options);
}
/** Internal hooks are deliberately excluded from the package entry point. */
export interface EventConsumerTransactionHooks {
  beforeHandler?: (tx: EventConsumerTransaction) => Promise<void>;
  afterReceipt?: (tx: EventConsumerTransaction) => Promise<void>;
  beforeCommit?: (tx: EventConsumerTransaction, receipt: EventConsumerTransactionResult) => Promise<void>;
  storageError?: () => Error;
}
export function eventConsumerDigests(input: EventConsumerRequest): { identity: Buffer; request: Buffer } {
  const original = requests.get(input);
  if (!original) throw new TypeError('use an event consumer request constructor');
  return {
    identity: createHash('sha256').update(['gregale.event-consumer.v1', input.accountId, input.appId,
      input.subscriptionId, input.acceptanceId].join('\n')).digest(),
    request: createHash('sha256').update('gregale.event-consumer-input.v1\n').update(original).digest(),
  };
}
export async function eventConsumerTransaction(
  pool: EventConsumerPool, input: EventConsumerRequest, handler: (transaction: EventConsumerTransaction) => Promise<string>,
  options: EventConsumerTransactionOptions = {}, hooks: EventConsumerTransactionHooks = {},
): Promise<EventConsumerTransactionResult> {
  const signal = options.signal;
  if (signal !== undefined && !(signal instanceof AbortSignal)) throw new TypeError('invalid event consumer signal');
  signal?.throwIfAborted();
  const original = requests.get(input);
  if (!original || typeof handler !== 'function') throw new TypeError('use an event consumer request constructor');
  const { identity: identityDigest, request: requestDigest } = eventConsumerDigests(input);
  const connection = await pool.connect().catch(error => { throw hooks.storageError?.() ?? error; });
  const query: EventConsumerTransaction['query'] = async (sql, values) => {
    signal?.throwIfAborted();
    const result = await connection.query(sql, values).catch(error => { throw hooks.storageError?.() ?? error; });
    signal?.throwIfAborted();
    return result;
  };
  let discard = false;
  try {
    await query('BEGIN ISOLATION LEVEL READ COMMITTED');
    await query('SELECT pg_advisory_xact_lock(hashtextextended($1, 0))', ['gregale.event-consumer-inbox.v1:' + identityDigest.toString('hex')]);
    const stored = (await query(`SELECT account_id::text,app_id::text,subscription_id::text,acceptance_id::text,event_source,event_id,request_digest,response_body FROM public.gregale_event_consumer_inbox WHERE identity_digest=$1`, [identityDigest])).rows[0];
    let body: string;
    if (stored) {
      if (stored.account_id !== input.accountId || stored.app_id !== input.appId || stored.subscription_id !== input.subscriptionId
          || stored.acceptance_id !== input.acceptanceId || stored.event_source !== input.source || stored.event_id !== input.eventId
          || !(stored.request_digest instanceof Uint8Array) || !requestDigest.equals(Buffer.from(stored.request_digest))) throw new EventConsumerConflictError();
      if (typeof stored.response_body !== 'string') throw new TypeError('invalid saved event response');
      body = stored.response_body;
      try { validateResult(body); } catch (error) { throw hooks.storageError?.() ?? error; }
    } else {
      signal?.throwIfAborted();
      await hooks.beforeHandler?.(Object.freeze({ query }));
      signal?.throwIfAborted();
      body = await handler(Object.freeze({ query }));
      signal?.throwIfAborted();
      validateResult(body);
      await query(`INSERT INTO public.gregale_event_consumer_inbox(identity_digest,account_id,app_id,subscription_id,acceptance_id,event_source,event_id,request_digest,response_body) VALUES ($1,$2::uuid,$3::uuid,$4::uuid,$5::bigint,$6,$7,$8,$9)`,
        [identityDigest, input.accountId, input.appId, input.subscriptionId, input.acceptanceId, input.source, input.eventId, requestDigest, body]);
      await hooks.afterReceipt?.(Object.freeze({ query }));
    }
    const receipt = { body, replayed: stored !== undefined };
    await hooks.beforeCommit?.(Object.freeze({ query }), Object.freeze({ ...receipt }));
    // Once COMMIT is sent, cancellation cannot establish rollback. Preserve a
    // confirmed result or the existing unknown-commit error even after abort.
    signal?.throwIfAborted();
    try { await connection.query('COMMIT'); }
    catch (error) { discard = true; throw new EventConsumerCommitUnknownError(hooks.storageError?.() ?? error); }
    return receipt;
  } catch (error) {
    try { await connection.query('ROLLBACK'); } catch { discard = true; }
    throw error;
  } finally { connection.release(discard); }
}
