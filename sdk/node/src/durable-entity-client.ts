/** Typed caller contracts are separate from guest handlers and contain no storage credentials. */
import {durableEntityHandle, type DurableEntityHandleScope} from './durable-entity-handle.js';
import {DURABLE_ENTITY_MAX_IDENTITY_BYTES} from './durable-entity-contract.js';
import {durableEntityJSON} from './durable-entity-handler.js';

export interface DurableEntityClientContract<Payload, Result> {
  readonly namespace: string;
  readonly decodePayload: (value: unknown) => Payload;
  readonly decodeResult: (value: unknown) => Result;
}
export function defineDurableEntityClientContract<Payload, Result>(contract: DurableEntityClientContract<Payload, Result>): DurableEntityClientContract<Payload, Result> {
  if (typeof contract.namespace !== 'string' || !contract.namespace.trim() || Buffer.from(contract.namespace).toString('utf8') !== contract.namespace || Buffer.byteLength(contract.namespace) > DURABLE_ENTITY_MAX_IDENTITY_BYTES || /[\x00-\x1f\x7f]/u.test(contract.namespace) || typeof contract.decodePayload !== 'function' || typeof contract.decodeResult !== 'function') throw new TypeError('invalid entity client contract');
  return Object.freeze({...contract});
}
/** No automatic replay: retry an uncertain invocation with the identical ID and payload. */
export function durableEntityClient<Payload, Result>(contract: DurableEntityClientContract<Payload, Result>, scope: Omit<DurableEntityHandleScope, 'namespace'>) {
  const captured = defineDurableEntityClientContract(contract);
  const handle = durableEntityHandle<Payload, Result>({...scope, namespace: captured.namespace}, value => {
    const result = captured.decodeResult(value);
    if (result !== null && typeof result === 'object' && typeof (result as {then?: unknown}).then === 'function') throw new TypeError('result decoder must be synchronous');
    return result;
  });
  return Object.freeze({
    ...handle,
    async invoke(requestId: string, payload: Payload) {
      if (typeof requestId !== 'string' || !requestId.trim() || Buffer.byteLength(requestId) > DURABLE_ENTITY_MAX_IDENTITY_BYTES || /[\x00-\x1f\x7f]/u.test(requestId) || requestId.startsWith('__gregale_alarm/')) throw new TypeError('explicit nonreserved request ID required');
      const snapshot: unknown = JSON.parse(durableEntityJSON(payload));
      // Validate before transport. Decoders must be synchronous and pure; validation never changes the wire payload.
      const checked = captured.decodePayload(JSON.parse(durableEntityJSON(snapshot)) as unknown);
      if (checked !== null && typeof checked === 'object' && typeof (checked as {then?: unknown}).then === 'function') throw new TypeError('payload decoder must be synchronous');
      durableEntityJSON(checked);
      return handle.invoke(requestId, snapshot as Payload);
    },
  });
}
