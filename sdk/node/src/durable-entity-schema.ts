// ADR-939: application schema versions, distinct from business commit versions.
import { decodeDurableEntityCall, type DurableEntityCall, type DurableEntityCallOptions, DurableEntityTransitionBuilder } from './durable-entity-call.js';
import { decodeDurableEntityHandlerRequest, durableEntityJSON, encodeDurableEntityTransition } from './durable-entity-handler.js';

export interface DurableEntitySchemaState<State> { schema_version: number; data: State }
export interface DurableEntityStateSchema<State> {
  version: number;
  initialState: () => State;
  decodeState: (value: unknown) => State;
  /** migrations.get(n) upgrades data from n to n+1 without I/O. */
  migrations: ReadonlyMap<number, (value: unknown) => unknown>;
  /** Explicit starting version for unwrapped committed data; absent rejects it. */
  legacyVersion?: number;
}

export interface DurableEntitySchemaCall<State, Payload> extends Omit<DurableEntityCall<State, Payload>, 'transition'> {
  readonly storedSchemaVersion: number;
  readonly migrated: boolean;
  transition<Result>(data: State, result: Result): DurableEntityTransitionBuilder<DurableEntitySchemaState<State>, Result>;
}

function version(value: unknown, allowZero = false): value is number {
  // Matches the Go schema version's uint32 representation, not a plan quota.
  return typeof value === 'number' && Number.isInteger(value) && value >= (allowZero ? 0 : 1) && value <= 0xffffffff;
}
function invalid(): never { throw new TypeError('invalid, unsupported or incomplete entity state schema'); }

export function decodeDurableEntitySchemaCall<State, Payload>(body: string | Uint8Array, schema: DurableEntityStateSchema<State>, decodePayload: DurableEntityCallOptions<State, Payload>['decodePayload']): DurableEntitySchemaCall<State, Payload> {
  if (!version(schema.version) || schema.legacyVersion !== undefined && !version(schema.legacyVersion, true)) return invalid();
  const target = schema.version;
  const legacyVersion = schema.legacyVersion;
  const migrations = new Map(schema.migrations);
  const decodeState = schema.decodeState;
  const initialState = schema.initialState;
  const request = decodeDurableEntityHandlerRequest(body);
  let source = target;
  let legacy = false;
  const call = decodeDurableEntityCall<DurableEntitySchemaState<State>, Payload>(body, {
    initialState: () => ({ schema_version: target, data: initialState() }),
    decodePayload,
    decodeState(value: unknown): DurableEntitySchemaState<State> {
      let data: unknown;
      if (value !== null && typeof value === 'object' && !Array.isArray(value) && Object.hasOwn(value, 'schema_version')) {
        const envelope = value as Record<string, unknown>;
        if (Object.keys(envelope).length !== 2 || !Object.hasOwn(envelope, 'data') || !version(envelope.schema_version)) return invalid();
        source = envelope.schema_version;
        data = envelope.data;
      } else {
        if (legacyVersion === undefined) return invalid();
        source = legacyVersion;
        legacy = true;
        data = value;
      }
      if (source > target || target - source > migrations.size) return invalid();
      // Check the whole chain before running any callback.
      for (let n = source; n < target; n++) if (typeof migrations.get(n) !== 'function') return invalid();
      for (let n = source; n < target; n++) {
        data = migrations.get(n)!(JSON.parse(durableEntityJSON(data)) as unknown);
        // Reject async/non-JSON transformations and enforce negotiated intermediate bounds.
        if (data !== null && (typeof data === 'object' || typeof data === 'function') && typeof (data as { then?: unknown }).then === 'function') return invalid();
        data = (JSON.parse(encodeDurableEntityTransition(request, { data, result: null })) as { data: unknown }).data;
      }
      return { schema_version: target, data: decodeState(data) };
    },
  });
  const wrap = (data: State): DurableEntitySchemaState<State> => ({ schema_version: target, data });
  encodeDurableEntityTransition(request, { data: call.state, result: null });
  return Object.freeze({
    ...call, state: call.state.data, storedSchemaVersion: source, migrated: request.state.version > 0 && (source < target || legacy),
    transition<Result>(data: State, result: Result) { return new DurableEntityTransitionBuilder(request, wrap(data), result, value => {
      if (value.schema_version !== target) return invalid();
      return wrap(decodeState(JSON.parse(durableEntityJSON(value.data)) as unknown));
    }); },
  });
}
