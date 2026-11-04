import {decodeDurableSessionCommand, decodeDurableSessionLifecycleCommand} from '@gregale/sdk-node/preview/durable-entities/session';
// Caller-only contracts: no handlers, registry, server or simulator imports.
import {defineDurableEntityClientContract} from '@gregale/sdk-node/preview/durable-entities/client';
import {counterState, reservationState, sessionState, record} from './state.mjs';
export const counterContract = defineDurableEntityClientContract({namespace: 'counters',
  decodePayload(value) {
    const input = record(value);
    if (Object.keys(input).length !== 1 || !Number.isSafeInteger(input.delta)) throw new TypeError('delta must be an integer');
    return {delta: /** @type {number} */ (input.delta)};
  }, decodeResult: counterState,
});
export const reservationContract = defineDurableEntityClientContract({namespace: 'reservations',
  decodePayload(value) {
    const input = record(value);
    if (Object.keys(input).length !== 3 || input.action !== 'reserve' || !Number.isSafeInteger(input.quantity) || /** @type {number} */ (input.quantity) <= 0 || typeof input.webhookId !== 'string' || !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/u.test(input.webhookId)) throw new TypeError('invalid reservation input');
    return {action: /** @type {'reserve'} */ (input.action), quantity: /** @type {number} */ (input.quantity), webhookId: input.webhookId};
  }, decodeResult: reservationState,
});
export const sessionContract = defineDurableEntityClientContract({namespace: 'sessions',
  decodePayload(value) {
    const input = record(value);
    if (Object.keys(input).length !== 2 || input.action !== 'start' || typeof input.at !== 'string' || !Number.isFinite(Date.parse(input.at))) throw new TypeError('provide an explicit alarm deadline');
    return {action: /** @type {'start'} */ (input.action), at: input.at};
  }, decodeResult: sessionState,
});

export const roomContract = defineDurableEntityClientContract({namespace: 'rooms',
  decodePayload(value) {
    const command=decodeDurableSessionCommand(value);
    if (command.kind==='message') {
      if (typeof command.payload!=='number' || !Number.isSafeInteger(command.payload)) throw new TypeError('integer room delta required');
      return {...command,payload:command.payload};
    }
    return command;
  },
  decodeResult(value) {
    const result=record(value);
    if (typeof result.session_id !== 'string' || !Number.isSafeInteger(result.sequence) || typeof result.closed !== 'boolean' || (result.value !== null && typeof result.value !== 'number')) throw new TypeError('invalid session result');
    return {session_id:result.session_id,sequence:/** @type {number} */ (result.sequence),closed:result.closed,value:/** @type {number|null} */ (result.value)};
  },
});

export const recoverableRoomContract = defineDurableEntityClientContract({namespace: 'recoverable-rooms',
  decodePayload(value) {
    const command = decodeDurableSessionLifecycleCommand(value);
    if (command.kind === 'message') {
      if (typeof command.payload !== 'number' || !Number.isSafeInteger(command.payload)) throw new TypeError('integer room delta required');
      return {...command, payload: command.payload};
    }
    return command;
  },
  decodeResult(value) {
    const result = record(value);
    if (!Number.isSafeInteger(result.generation) || /** @type {number} */ (result.generation) < 0) throw new TypeError('invalid generation');
    const generation = /** @type {number} */ (result.generation);
    if (result.kind === 'advance' || result.kind === 'reclaim') {
      if (!Number.isSafeInteger(result.tick) || /** @type {number} */ (result.tick) < 0) throw new TypeError('invalid tick');
      return {kind: result.kind, generation, tick: /** @type {number} */ (result.tick)};
    }
    if (result.kind === 'status') {
      if (typeof result.session_id !== 'string' || typeof result.query_id !== 'string' || !Number.isSafeInteger(result.tick) || /** @type {number} */ (result.tick) < 0 || typeof result.exists !== 'boolean'
        || (result.exists ? !Number.isSafeInteger(result.sequence) || /** @type {number} */ (result.sequence) < 0 || typeof result.closed !== 'boolean' || typeof result.expired !== 'boolean' || result.expired && !result.closed : result.sequence !== null || result.closed !== null || result.expired !== null)) throw new TypeError('invalid status');
      return {kind: result.kind, generation, tick: /** @type {number} */ (result.tick), session_id: result.session_id, query_id: result.query_id, exists: result.exists,
        sequence: /** @type {number|null} */ (result.sequence), closed: /** @type {boolean|null} */ (result.closed), expired: /** @type {boolean|null} */ (result.expired)};
    }
    if (!['open','message','close'].includes(/** @type {string} */ (result.kind)) || typeof result.session_id !== 'string' || !Number.isSafeInteger(result.sequence) || /** @type {number} */ (result.sequence) < 0 || typeof result.closed !== 'boolean' || result.expired !== false || result.closed !== (result.kind === 'close')
      || (result.kind === 'message' ? typeof result.value !== 'number' || !Number.isSafeInteger(result.value) : result.value !== null)) throw new TypeError('invalid ordered result');
    return {kind: /** @type {'open'|'message'|'close'} */ (result.kind), generation, session_id: result.session_id, sequence: /** @type {number} */ (result.sequence), closed: result.closed, expired: false, value: /** @type {number|null} */ (result.value)};
  },
});
