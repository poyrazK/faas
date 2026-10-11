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
