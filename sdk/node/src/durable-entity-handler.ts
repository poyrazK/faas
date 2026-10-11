// ADR-934. Guest helpers describe pure transitions; they never commit or send.
import {
  DURABLE_ENTITY_PROTOCOL_VERSION, DURABLE_ENTITY_OUTBOX_PROTOCOL_VERSION,
  DURABLE_ENTITY_MAX_REQUEST_BYTES, DURABLE_ENTITY_MAX_TRANSITION_BYTES,
} from './durable-entity-contract.js';

export interface DurableEntityIdentity {
  account_id: string;
  app_id: string;
  environment_id?: string;
  tenant_id?: string;
  namespace: string;
  key: string;
}

/** Advertised central bounds. The platform also checks the full pending queue. */
export interface DurableEntityHandlerLimits {
  transition_bytes: number;
  identity_bytes: number;
  outbox_messages: number;
  outbox_payload_bytes: number;
  outbox_bytes: number;
}

export interface DurableEntityHandlerRequest<State = unknown, Payload = unknown> {
  protocol_version: 1 | 2;
  event?: 'invoke' | 'alarm' | '';
  entity: DurableEntityIdentity;
  request_id: string;
  payload: Payload;
  state: { data: State; version: number; alarm_at?: string };
  deployment_id: string;
  limits?: DurableEntityHandlerLimits;
}

export interface DurableEntityWebhookIntent<Payload = unknown> {
  webhook_id: string;
  event_type: string;
  payload: Payload;
}

/** Omit alarm_at to clear the alarm; omit outbox to preserve pending messages. */
export interface DurableEntityTransition<State = unknown, Result = unknown> {
  data: State;
  result: Result;
  alarm_at?: string;
  outbox?: DurableEntityWebhookIntent[];
}

function invalid(): never { throw new TypeError('invalid or unsupported durable entity handler contract'); }

function record(value: unknown, keys: readonly string[]): Record<string, unknown> {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return invalid();
  const result = value as Record<string, unknown>;
  if (Object.keys(result).some(key => !keys.includes(key))) return invalid();
  return result;
}

function identity(value: unknown, maximum?: number): value is string {
  return typeof value === 'string' && value.trim() !== '' && !/[\x00-\x1f\x7f]/u.test(value)
    && Buffer.from(value).toString('utf8') === value && (maximum === undefined || Buffer.byteLength(value) <= maximum);
}

/** @internal Shared strict JSON serialization for SDK helpers. */
export function durableEntityJSON(value: unknown): string {
  const body = JSON.stringify(value, (_key, item: unknown) => {
    if (item === undefined || typeof item === 'function' || typeof item === 'symbol' || typeof item === 'bigint'
      || typeof item === 'number' && !Number.isFinite(item)
      || typeof item === 'string' && Buffer.from(item).toString('utf8') !== item) return invalid();
    return item;
  });
  if (body === undefined) return invalid();
  // Match encoding/json's snapshot representation when applying byte budgets.
  return body.replace(/[<>&\u2028\u2029]/gu, value => `\\u${value.charCodeAt(0).toString(16).padStart(4, '0')}`);
}

function alarm(value: unknown): boolean {
  if (value === undefined) return true;
  if (typeof value !== 'string' || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/u.test(value)) return false;
  const clock = value.slice(11, 19).split(':').map(Number);
  if (clock[0]! >= 24 || clock[1]! >= 60 || clock[2]! >= 60) return false;
  const date = new Date(value);
  if (!Number.isFinite(date.getTime()) || date.getUTCFullYear() < 1 || date.getUTCFullYear() > 9999
    || date.getTime() === new Date('0001-01-01T00:00:00Z').getTime()) return false;
  const calendar = new Date(`${value.slice(0, 10)}T00:00:00Z`);
  return Number.isFinite(calendar.getTime()) && calendar.toISOString().slice(0, 10) === value.slice(0, 10);
}

function validateRequest(value: unknown): DurableEntityHandlerRequest {
  const request = record(value, ['protocol_version', 'event', 'entity', 'request_id', 'payload', 'state', 'deployment_id', 'limits']);
  if (request.protocol_version !== DURABLE_ENTITY_PROTOCOL_VERSION && request.protocol_version !== DURABLE_ENTITY_OUTBOX_PROTOCOL_VERSION
    || request.event !== undefined && request.event !== '' && request.event !== 'invoke' && request.event !== 'alarm'
    || !Object.hasOwn(request, 'payload')) return invalid();
  let maximum: number | undefined;
  if (request.protocol_version === DURABLE_ENTITY_OUTBOX_PROTOCOL_VERSION) {
    const limits = record(request.limits, ['transition_bytes', 'identity_bytes', 'outbox_messages', 'outbox_payload_bytes', 'outbox_bytes']);
    for (const key of ['transition_bytes', 'identity_bytes', 'outbox_messages', 'outbox_payload_bytes', 'outbox_bytes']) {
      if (!Number.isSafeInteger(limits[key]) || (limits[key] as number) <= 0) return invalid();
    }
    if ((limits.transition_bytes as number) > DURABLE_ENTITY_MAX_TRANSITION_BYTES
      || (limits.outbox_bytes as number) < (limits.outbox_payload_bytes as number)
      || (limits.transition_bytes as number) < (limits.outbox_bytes as number)) return invalid();
    maximum = limits.identity_bytes as number;
  } else if (request.limits !== undefined) return invalid();
  const entity = record(request.entity, ['account_id', 'app_id', 'environment_id', 'tenant_id', 'namespace', 'key']);
  for (const key of ['account_id', 'app_id', 'namespace', 'key']) if (!identity(entity[key], maximum)) return invalid();
  for (const key of ['environment_id', 'tenant_id']) {
    if (entity[key] !== undefined && entity[key] !== '' && !identity(entity[key], maximum)) return invalid();
  }
  if (!identity(request.request_id, maximum) || !identity(request.deployment_id, maximum)) return invalid();
  const state = record(request.state, ['data', 'version', 'alarm_at']);
  if (!Object.hasOwn(state, 'data') || !Number.isSafeInteger(state.version) || (state.version as number) < 0 || !alarm(state.alarm_at)) return invalid();
  durableEntityJSON(state.data);
  durableEntityJSON(request.payload);
  return request as unknown as DurableEntityHandlerRequest;
}

/** Bound the HTTP body first. Parsing is not authentication for a public route. */
export function decodeDurableEntityHandlerRequest<State = unknown, Payload = unknown>(body: string | Uint8Array): DurableEntityHandlerRequest<State, Payload> {
  const text = typeof body === 'string' ? body : Buffer.from(body).toString('utf8');
  if (Buffer.byteLength(text) > DURABLE_ENTITY_MAX_REQUEST_BYTES) return invalid();
  if (typeof body !== 'string' && !Buffer.from(text).equals(Buffer.from(body))) return invalid();
  return validateRequest(JSON.parse(text)) as DurableEntityHandlerRequest<State, Payload>;
}

function validateIntent(value: unknown, limits: DurableEntityHandlerLimits): void {
  const intent = record(value, ['webhook_id', 'event_type', 'payload']);
  if (typeof intent.webhook_id !== 'string' || !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/u.test(intent.webhook_id)
    || intent.webhook_id === '00000000-0000-0000-0000-000000000000'
    || !identity(intent.event_type, limits.identity_bytes) || !Object.hasOwn(intent, 'payload')
    || Buffer.byteLength(durableEntityJSON(intent.payload)) > limits.outbox_payload_bytes) return invalid();
}

/** Requires negotiated v2 and a registered app webhook ID, never a target URL. */
export function durableEntityWebhookIntent<Payload>(input: DurableEntityHandlerRequest, webhookID: string, eventType: string, payload: Payload): DurableEntityWebhookIntent<Payload> {
  const request = validateRequest(input);
  if (request.protocol_version !== DURABLE_ENTITY_OUTBOX_PROTOCOL_VERSION || request.limits === undefined) return invalid();
  const intent = { webhook_id: webhookID, event_type: eventType, payload };
  validateIntent(intent, request.limits);
  // Capture JSON now so a reused caller buffer cannot alter the queued intent.
  return JSON.parse(durableEntityJSON(intent)) as DurableEntityWebhookIntent<Payload>;
}

/** Produces response bytes only. Guest HTTP success does not prove publication. */
export function encodeDurableEntityTransition(input: DurableEntityHandlerRequest, next: DurableEntityTransition): string {
  const request = validateRequest(input);
  const transition = record(next, ['data', 'result', 'alarm_at', 'outbox']);
  if (!Object.hasOwn(transition, 'data') || !Object.hasOwn(transition, 'result') || !alarm(transition.alarm_at)) return invalid();
  if (request.protocol_version === DURABLE_ENTITY_PROTOCOL_VERSION && Object.hasOwn(transition, 'outbox')) return invalid();
  const limits = request.limits;
  if (request.protocol_version === DURABLE_ENTITY_OUTBOX_PROTOCOL_VERSION && limits !== undefined && Object.hasOwn(transition, 'outbox')) {
    if (!Array.isArray(transition.outbox) || transition.outbox.length > limits.outbox_messages) return invalid();
    for (const intent of transition.outbox) validateIntent(intent, limits);
    if (Buffer.byteLength(durableEntityJSON(transition.outbox)) > limits.outbox_bytes) return invalid();
  }
  const body = durableEntityJSON(transition);
  if (Buffer.byteLength(body) > DURABLE_ENTITY_MAX_TRANSITION_BYTES
    || limits !== undefined && Buffer.byteLength(body) > limits.transition_bytes) return invalid();
  return body;
}
