/** Pure, namespace-bound guest registration. This module has no client or credentials. */
import { decodeDurableEntityHandlerRequest, durableEntityJSON, encodeDurableEntityTransition } from './durable-entity-handler.js';
import { decodeDurableEntitySchemaCall, type DurableEntityStateSchema, type DurableEntitySchemaCall } from './durable-entity-schema.js';
import { type DurableEntityTransitionBuilder } from './durable-entity-call.js';
import { DURABLE_ENTITY_MAX_IDENTITY_BYTES } from './durable-entity-contract.js';
import { encodeDurableEntityRestoreValidation } from './durable-entity-restore-validation.js';

export interface DurableEntityDefinition {
  readonly namespace: string;
  readonly schemaVersion: number;
  handle(body: string | Uint8Array): string;
  validateCandidate(candidate: unknown): boolean;
}

export function defineDurableEntity<State, Payload, Result>(options: {
  namespace: string;
  schema: DurableEntityStateSchema<State>;
  decodePayload: (value: unknown, event: 'invoke' | 'alarm') => Payload;
  handle: (call: DurableEntitySchemaCall<State, Payload>) => DurableEntityTransitionBuilder<{schema_version: number; data: State}, Result>;
}): DurableEntityDefinition {
  const {namespace, decodePayload, handle} = options;
  if (typeof namespace !== 'string' || Buffer.from(namespace).toString('utf8') !== namespace || !namespace.trim() || /[\x00-\x1f\x7f]/u.test(namespace) || Buffer.byteLength(namespace) > DURABLE_ENTITY_MAX_IDENTITY_BYTES
    || !Number.isInteger(options.schema.version) || options.schema.version <= 0 || options.schema.version > 0xffffffff) {
    throw new TypeError('invalid durable entity definition');
  }
  const schema = Object.freeze({...options.schema, migrations: new Map(options.schema.migrations)});
  return Object.freeze({
    namespace, schemaVersion: schema.version,
    handle(body: string | Uint8Array): string {
      const request = decodeDurableEntityHandlerRequest(body);
      if (request.entity.namespace !== namespace) throw new TypeError('unregistered entity namespace');
      const call = decodeDurableEntitySchemaCall(body, schema, decodePayload);
      // Promise-returning handlers are intentionally unsupported. Computation must be pure.
      return encodeDurableEntityTransition(request, JSON.parse(handle(call).encode()));
    },
    validateCandidate(candidate: unknown): boolean {
      try {
        const value = JSON.parse(durableEntityJSON(candidate)) as Record<string, unknown>;
        if (value === null || typeof value !== 'object' || Array.isArray(value)
          || Object.keys(value).length !== 2 || value.schema_version !== schema.version || !Object.hasOwn(value, 'data')) return false;
        const decoded = schema.decodeState(value.data);
        if (decoded !== null && typeof decoded === 'object' && typeof (decoded as {then?: unknown}).then === 'function') return false;
        durableEntityJSON(decoded);
        return true;
      } catch { return false; }
    },
  });
}

export function createDurableEntityRegistry(definitions: readonly DurableEntityDefinition[]) {
  const registered = new Map<string, DurableEntityDefinition>();
  for (const definition of definitions) {
    if (registered.has(definition.namespace)) throw new TypeError('duplicate entity namespace');
    // Capture methods and metadata rather than retaining a mutable registration object.
    registered.set(definition.namespace, Object.freeze({...definition}));
  }
  if (registered.size === 0) throw new TypeError('at least one entity definition is required');
  return Object.freeze({
    describe() { return [...registered.values()].map(({namespace, schemaVersion}) => ({namespace, schemaVersion})); },
    handle(body: string | Uint8Array): string {
      const request = decodeDurableEntityHandlerRequest(body);
      const definition = registered.get(request.entity.namespace);
      if (!definition) throw new TypeError('unregistered entity namespace');
      return definition.handle(body);
    },
    validateRestore(body: string | Uint8Array): string {
      return encodeDurableEntityRestoreValidation(body, request => registered.get(request.entity.namespace)?.validateCandidate(request.candidate) === true);
    },
  });
}

export type DurableEntityRegistry = ReturnType<typeof createDurableEntityRegistry>;
export { decodeDurableEntityCall, DurableEntityTransitionBuilder } from './durable-entity-call.js';
export { decodeDurableEntitySchemaCall } from './durable-entity-schema.js';
export * from './durable-entity-contract.js';
