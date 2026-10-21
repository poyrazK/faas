import { eventConsumerRequestFromHeaders, type EventConsumerIdentity } from './event-consumer-transactions.js';

export interface EventConsumerDeliveryContext {
  readonly identity: EventConsumerIdentity;
  readonly invocationId: string;
  readonly source: 'async_invoke' | 'replay';
  readonly attempt: number;
  /** Canonical nonnegative int64 string; never round through a JS number. */
  readonly replayGeneration: string;
  readonly replayedFromInvocationId?: string;
  readonly replayRootInvocationId?: string;
}
export class EventConsumerDeliveryContextUnavailableError extends Error {
  constructor() { super('event delivery attempt metadata unavailable; upgrade scheduler and gateway'); this.name = 'EventConsumerDeliveryContextUnavailableError'; }
}
const uuid = (raw: string): string => {
  if (raw.length !== 36 || !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(raw) || raw === '00000000-0000-0000-0000-000000000000') throw new TypeError('invalid event delivery identity');
  return raw.toLowerCase();
};
/** Authenticate the route and supply owner scope first. Headers are not signatures.
 * Attempt/lineage context is diagnostic; do not add it to business receipt identity. */
export function eventConsumerDeliveryContextFromHeaders(
  headers: Record<string, string | readonly string[] | undefined>, body: Uint8Array, accountId: string, appId: string,
): EventConsumerDeliveryContext {
  const request = eventConsumerRequestFromHeaders(headers, body, accountId, appId);
  const read = (name: string): string | undefined => {
    const entries = Object.entries(headers).filter(([key]) => key.toLowerCase() === name);
    if (entries.length === 0) return undefined;
    if (entries.length !== 1) throw new TypeError('duplicate event attempt context');
    const values = entries.flatMap(([, value]) => value === undefined ? [] : typeof value === 'string' ? [value] : [...value]);
    if (values.length !== 1 || !values[0]) throw new TypeError('missing or duplicate event attempt context');
    return values[0];
  };
  const attemptRaw = read('x-faas-invocation-attempt');
  const generation = read('x-faas-invocation-replay-generation');
  const parentRaw = read('x-faas-invocation-replayed-from');
  const rootRaw = read('x-faas-invocation-replay-root');
  const idRaw = read('x-faas-invocation-id');
  const source = read('x-faas-invocation-source');
  if (attemptRaw === undefined && generation === undefined && parentRaw === undefined && rootRaw === undefined) throw new EventConsumerDeliveryContextUnavailableError();
  const id = uuid(idRaw ?? '');
  if (source !== 'async_invoke' && source !== 'replay') throw new TypeError('invalid event delivery source');
  if (attemptRaw === undefined || !/^[1-9][0-9]{0,9}$/.test(attemptRaw) || BigInt(attemptRaw) > 2147483647n || BigInt(attemptRaw).toString() !== attemptRaw) throw new TypeError('invalid event delivery attempt');
  if (generation === undefined || !/^(0|[1-9][0-9]{0,18})$/.test(generation) || BigInt(generation) > 9223372036854775807n || BigInt(generation).toString() !== generation) throw new TypeError('invalid event replay generation');
  const parent = parentRaw === undefined ? undefined : uuid(parentRaw);
  const root = rootRaw === undefined ? undefined : uuid(rootRaw);
  if (parent === id || (parent !== undefined && root === id)) throw new TypeError('cyclic event delivery lineage');
  const identity = Object.freeze({ accountId: request.accountId, appId: request.appId, subscriptionId: request.subscriptionId,
    acceptanceId: request.acceptanceId, source: request.source, eventId: request.eventId });
  return Object.freeze({ identity, invocationId: id, source, attempt: Number(attemptRaw), replayGeneration: generation,
    ...(parent === undefined ? {} : { replayedFromInvocationId: parent }), ...(root === undefined ? {} : { replayRootInvocationId: root }) });
}
