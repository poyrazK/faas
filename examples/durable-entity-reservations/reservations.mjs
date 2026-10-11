import { durableEntityWebhookIntent, encodeDurableEntityTransition, DURABLE_ENTITY_OUTBOX_PROTOCOL_VERSION } from './sdk.mjs';

// A pure computation: one entity stores one reservation, and Gregale commits
// its state and confirmation intent together. No notification is sent here.
export function reservationTransition(call, webhookID) {
  if (call.protocol_version !== DURABLE_ENTITY_OUTBOX_PROTOCOL_VERSION || call.event !== 'invoke' || call.entity.namespace !== 'reservations'
    || call.payload === null || call.payload.action !== 'reserve'
    || !Number.isSafeInteger(call.payload.quantity) || call.payload.quantity <= 0) {
    throw new TypeError('invalid reservation request');
  }
  const initial = call.state.version === 0 && call.state.data !== null
    && typeof call.state.data === 'object' && !Array.isArray(call.state.data)
    && Object.keys(call.state.data).length === 0;
  const existing = initial ? null : call.state.data;
  if (existing !== null && (existing.status !== 'reserved' || !Number.isSafeInteger(existing.quantity)
    || existing.quantity <= 0 || existing.quantity !== call.payload.quantity)) {
    throw new TypeError('reservation conflicts with existing state');
  }
  const next = existing ?? { status: 'reserved', quantity: call.payload.quantity };
  const transition = { data: next, result: next };
  if (existing === null) {
    transition.outbox = [durableEntityWebhookIntent(call, webhookID, 'reservation.confirmed', {
      reservation_key: call.entity.key, quantity: next.quantity,
    })];
  }
  return encodeDurableEntityTransition(call, transition);
}
