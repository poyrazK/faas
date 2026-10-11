// Pure isolated validator: no imports from the SDK, credentials or server code.
import {counterState, reservationState, sessionState, roomState, recoverableRoomState} from './state.mjs';

export default function validateRestore(input) {
  let valid = false;
  try {
    const candidate = input?.candidate;
    const decode = new Map([['counters', counterState], ['reservations', reservationState], ['sessions', sessionState], ['rooms', roomState], ['recoverable-rooms', recoverableRoomState]]).get(input?.entity?.namespace);
    if (input?.protocol_version === 1 && input?.event === 'validate_restore' && decode
      && candidate?.schema_version === (input.entity.namespace === 'recoverable-rooms' ? 2 : 1) && Object.keys(candidate).length === 2 && Object.hasOwn(candidate, 'data')) {
      decode(candidate.data); valid = true;
    }
  } catch { valid = false; }
  return {protocol_version: 1, valid};
}
