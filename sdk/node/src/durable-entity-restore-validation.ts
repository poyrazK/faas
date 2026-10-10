// ADR-943: synchronous pure validator, with no transition or outgoing intents.
import { DURABLE_ENTITY_MAX_REQUEST_BYTES, DURABLE_ENTITY_MAX_TRANSITION_BYTES, DURABLE_ENTITY_RESTORE_VALIDATION_PROTOCOL_VERSION } from './durable-entity-contract.js';
import { durableEntityJSON, type DurableEntityIdentity } from './durable-entity-handler.js';

export interface DurableEntityRestoreValidationRequest {
  protocol_version: 1;
  event: 'validate_restore';
  entity: DurableEntityIdentity;
  request_id: string;
  deployment_id: string;
  expected_version: number;
  source_version: number;
  candidate: unknown;
}

function invalid(): never { throw new TypeError('invalid durable entity restore validation contract'); }
function object(value: unknown, keys: readonly string[]): Record<string, unknown> {
  if (value === null || typeof value !== 'object' || Array.isArray(value) || Object.keys(value).some(key => !keys.includes(key))) return invalid();
  return value as Record<string, unknown>;
}
function identity(value: unknown): value is string {
  return typeof value === 'string' && value.trim() !== '' && !/[\x00-\x1f\x7f]/u.test(value) && Buffer.from(value).toString('utf8') === value;
}

export function decodeDurableEntityRestoreValidationRequest(body: string | Uint8Array): DurableEntityRestoreValidationRequest {
  const bytes = typeof body === 'string' ? Buffer.from(body) : Buffer.from(body);
  const text = bytes.toString('utf8');
  if (bytes.length > DURABLE_ENTITY_MAX_REQUEST_BYTES || !bytes.equals(Buffer.from(text)) || typeof body === 'string' && text !== body) return invalid();
  const request = object(JSON.parse(text) as unknown, ['protocol_version', 'event', 'entity', 'request_id', 'deployment_id', 'expected_version', 'source_version', 'candidate']);
  const entity = object(request.entity, ['account_id', 'app_id', 'environment_id', 'tenant_id', 'namespace', 'key']);
  if (request.protocol_version !== DURABLE_ENTITY_RESTORE_VALIDATION_PROTOCOL_VERSION || request.event !== 'validate_restore'
    || !Number.isSafeInteger(request.expected_version) || (request.expected_version as number) <= 0
    || !Number.isSafeInteger(request.source_version) || (request.source_version as number) <= 0
    || !Object.hasOwn(request, 'candidate') || Buffer.byteLength(durableEntityJSON(request.candidate)) > DURABLE_ENTITY_MAX_TRANSITION_BYTES) return invalid();
  for (const value of [entity.account_id, entity.app_id, entity.namespace, entity.key, request.request_id, request.deployment_id]) if (!identity(value)) return invalid();
  for (const value of [entity.environment_id, entity.tenant_id]) if (value !== undefined && !identity(value)) return invalid();
  return request as unknown as DurableEntityRestoreValidationRequest;
}

/** Validation must be synchronous and perform no I/O. Errors expose no details. */
export function encodeDurableEntityRestoreValidation(body: string | Uint8Array, validate: (request: DurableEntityRestoreValidationRequest) => boolean): string {
  const request = decodeDurableEntityRestoreValidationRequest(body);
  let valid = false;
  try { valid = validate(request) === true; } catch { valid = false; }
  return JSON.stringify({ protocol_version: DURABLE_ENTITY_RESTORE_VALIDATION_PROTOCOL_VERSION, valid });
}
