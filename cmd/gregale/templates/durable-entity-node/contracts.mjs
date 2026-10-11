// Caller-only contracts: no handlers, registry, server or simulator imports.
import {defineDurableEntityClientContract} from '@gregale/sdk-node/durable-entities/client';
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
