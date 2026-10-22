/** @param {unknown} value @returns {Record<string, unknown>} */
function record(value) {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) throw new TypeError('invalid state');
  return /** @type {Record<string, unknown>} */ (value);
}
/** @param {unknown} value */
export function counterState(value) {
  const state = record(value);
  if (!Number.isSafeInteger(state.count) || /** @type {number} */ (state.count) < 0 || Object.keys(state).length !== 1) throw new TypeError('invalid counter');
  return {count: /** @type {number} */ (state.count)};
}
/** @param {unknown} value */
export function reservationState(value) {
  const state = record(value);
  if ((state.status !== 'empty' && state.status !== 'reserved') || !Number.isSafeInteger(state.quantity)
    || (state.status === 'empty' ? state.quantity !== 0 : /** @type {number} */ (state.quantity) <= 0) || Object.keys(state).length !== 2) throw new TypeError('invalid reservation');
  return {status: /** @type {'empty' | 'reserved'} */ (state.status), quantity: /** @type {number} */ (state.quantity)};
}
/** @param {unknown} value */
export function sessionState(value) {
  const state = record(value);
  if (!['idle', 'waiting', 'done'].includes(/** @type {string} */ (state.status)) || !Number.isSafeInteger(state.steps)
    || /** @type {number} */ (state.steps) < 0 || Object.keys(state).length !== 2) throw new TypeError('invalid session');
  return {status: /** @type {'idle' | 'waiting' | 'done'} */ (state.status), steps: /** @type {number} */ (state.steps)};
}
export {record};
/** @param {unknown} value */
export function roomState(value) {
  const state = record(value);
  if (Object.keys(state).length !== 2 || !Array.isArray(state.sessions) || state.sessions.length > 32) throw new TypeError('invalid room state');
  const ids = new Set();
  const sessions = state.sessions.map(value => {
    const item = record(value);
    if (Object.keys(item).length !== 3 || typeof item.id !== 'string' || !/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/u.test(item.id) || ids.has(item.id) || !Number.isSafeInteger(item.sequence) || /** @type {number} */ (item.sequence) < 0 || typeof item.closed !== 'boolean') throw new TypeError('invalid room session');
    ids.add(item.id); return {id:item.id,sequence:/** @type {number} */ (item.sequence),closed:item.closed};
  });
  return {application:counterState(state.application),sessions};
}

/** @param {unknown} value */
export function recoverableRoomState(value) {
  const state = record(value);
  const integer = (/** @type {unknown} */ value) => Number.isSafeInteger(value) && /** @type {number} */ (value) >= 0;
  if (Object.keys(state).length !== 4 || !integer(state.generation) || !integer(state.tick) || !Array.isArray(state.sessions) || state.sessions.length > 32) throw new TypeError('invalid recoverable room');
  const ids = new Set();
  for (const value of state.sessions) {
    const item = record(value);
    if (Object.keys(item).length !== 6 || typeof item.id !== 'string' || !/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/u.test(item.id) || ids.has(item.id) || !integer(item.sequence)
      || typeof item.closed !== 'boolean' || typeof item.expired !== 'boolean' || !integer(item.active_at) || /** @type {number} */ (item.active_at) > /** @type {number} */ (state.tick)
      || (item.closed ? !integer(item.closed_at) || /** @type {number} */ (item.closed_at) < /** @type {number} */ (item.active_at) || /** @type {number} */ (item.closed_at) > /** @type {number} */ (state.tick) : item.closed_at !== null || item.expired)) throw new TypeError('invalid recoverable session');
    ids.add(item.id);
  }
  return {application: counterState(state.application), generation: state.generation, tick: state.tick, sessions: state.sessions};
}
