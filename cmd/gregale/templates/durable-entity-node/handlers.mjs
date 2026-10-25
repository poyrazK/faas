import {defineDurableSessionEntity, defineRecoverableDurableSessionEntity} from '@gregale/sdk-node/preview/durable-entities/session';
import {defineDurableEntity} from '@gregale/sdk-node/preview/durable-entities/runtime';
import {counterState, reservationState, sessionState} from './state.mjs';
import {counterContract, reservationContract, sessionContract} from './contracts.mjs';

export const counter = defineDurableEntity({
  namespace: counterContract.namespace,
  schema: {version: 1, initialState: () => ({count: 0}), decodeState: counterState, migrations: new Map()},
  decodePayload: counterContract.decodePayload,
  handle(call) {
    const next = counterState({count: call.state.count + call.payload.delta});
    return call.transition(next, next);
  },
});

export const reservation = defineDurableEntity({
  namespace: reservationContract.namespace,
  schema: {version: 1, initialState: () => reservationState({status: 'empty', quantity: 0}), decodeState: reservationState, migrations: new Map()},
  decodePayload: reservationContract.decodePayload,
  handle(call) {
    if (call.state.status === 'reserved') {
      if (call.state.quantity !== call.payload.quantity) throw new TypeError('reservation already has another quantity');
      return call.transition(call.state, call.state);
    }
    const next = reservationState({status: 'reserved', quantity: call.payload.quantity});
    return call.transition(next, next).webhook(call.payload.webhookId, 'reservation.confirmed', {key: call.entity.key, quantity: next.quantity});
  },
});

export const session = defineDurableEntity({
  namespace: sessionContract.namespace,
  schema: {version: 1, initialState: () => sessionState({status: 'idle', steps: 0}), decodeState: sessionState, migrations: new Map()},
  decodePayload(value, event) {
    if (event === 'alarm') return {action: /** @type {'start'} */ ('start'), at: ''};
    return sessionContract.decodePayload(value);
  },
  handle(call) {
    if (call.event === 'alarm') {
      const next = sessionState({status: 'done', steps: call.state.steps + 1});
      return call.transition(next, next).clearAlarm();
    }
    const next = sessionState({status: 'waiting', steps: call.state.steps + 1});
    return call.transition(next, next).scheduleAlarm(call.payload.at);
  },
});

// Ordered, reconnectable application sessions; sockets are only a transport.
export const room = defineDurableSessionEntity({namespace: 'rooms',
  initialState: () => ({count: 0}), decodeState: counterState,
  decodePayload(value) {if (typeof value !== 'number' || !Number.isSafeInteger(value)) throw new TypeError('integer delta required');return value;},
  handle({state, payload}) {const next=counterState({count:state.count+payload});return {state:next,result:next.count};},
});

// Service-controlled logical ticks: expire idle sessions, retain tombstones, then reclaim a closed cohort.
export const recoverableRoom = defineRecoverableDurableSessionEntity({namespace: 'recoverable-rooms',
  idleTicks: 10, retentionTicks: 5,
  initialState: () => ({count: 0}), decodeState: counterState,
  decodePayload(value) {if (typeof value !== 'number' || !Number.isSafeInteger(value)) throw new TypeError('integer delta required');return value;},
  handle({state, payload}) {const next=counterState({count:state.count+payload});return {state:next,result:next.count};},
});
