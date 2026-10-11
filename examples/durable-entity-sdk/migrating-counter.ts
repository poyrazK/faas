// Pure schema-aware computation. Uses the same private guest route as counter.ts.
import { decodeDurableEntitySchemaCall, encodeDurableEntityRestoreValidation, type DurableEntityStateSchema } from '@gregale/sdk-node';

type Counter = { count: number };
const schema: DurableEntityStateSchema<Counter> = {
  version: 2,
  initialState: () => ({ count: 0 }),
  decodeState(value: unknown): Counter {
    if (value === null || typeof value !== 'object' || !('count' in value)
      || !Number.isSafeInteger(value.count) || (value.count as number) < 0) throw new TypeError('invalid counter');
    return { count: value.count as number };
  },
  migrations: new Map<number, (value: unknown) => unknown>([[1, value => {
    if (value === null || typeof value !== 'object' || !('total' in value)
      || !Number.isSafeInteger(value.total)) throw new TypeError('invalid version-one counter');
    return { count: value.total };
  }]]),
};

export function migratingCounterTransition(body: string | Uint8Array): string {
  const call = decodeDurableEntitySchemaCall(body, schema, (value: unknown, event) => {
    if (event === 'alarm') return { delta: 0 };
    if (value === null || typeof value !== 'object' || !('delta' in value)
      || !Number.isSafeInteger(value.delta)) throw new TypeError('invalid counter input');
    return { delta: value.delta as number };
  });
  if (call.entity.namespace !== 'counters') throw new TypeError('unexpected entity namespace');
  if (call.event === 'alarm') return call.transition(call.state, null).clearAlarm().encode();
  const count = call.state.count + call.payload.delta;
  if (!Number.isSafeInteger(count) || count < 0) throw new RangeError('counter overflow or underflow');
  return call.transition({ count }, { count }).encode();
}

// Mount on the distinct private /__gregale/entities/validate-restore route.
// A verdict does not migrate data: this example accepts the exact current schema.
export function migratingCounterRestoreValidation(body: string | Uint8Array): string {
  return encodeDurableEntityRestoreValidation(body, request => {
    if (request.entity.namespace !== 'counters') return false;
    const candidate = request.candidate;
    if (candidate === null || typeof candidate !== 'object' || Array.isArray(candidate)
      || Object.keys(candidate).length !== 2 || !('schema_version' in candidate)
      || candidate.schema_version !== schema.version || !('data' in candidate)) return false;
    schema.decodeState(candidate.data);
    return true;
  });
}
