/** Create a UUIDv7 for the opt-in v2 replay contract. Capture this key once,
 * with the full payload and original API/identity, before sending a mutation.
 * Its timestamp is immutable: retries must keep the entire original key.
 * The database clock decides expiry; generating a key grants no retry safety.
 */
export function createReplayKeyV2(): string {
  const milliseconds = Date.now()
  if (!Number.isSafeInteger(milliseconds) || milliseconds < 0 || milliseconds >= 2 ** 48) {
    throw new RangeError('A valid UUIDv7 clock is required')
  }
  const bytes = crypto.getRandomValues(new Uint8Array(16))
  let timestamp = BigInt(milliseconds)
  for (let i = 5; i >= 0; i--) {
    bytes[i] = Number(timestamp & 255n)
    timestamp >>= 8n
  }
  bytes[6] = (bytes[6] & 15) | 112
  bytes[8] = (bytes[8] & 63) | 128
  const hex = Array.from(bytes, byte => byte.toString(16).padStart(2, '0')).join('')
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
}

/** Exact server contract; a generic HTTP 410 must not imply replay expiry. */
export function isReplayExpired(error: unknown): boolean {
  return error !== null && typeof error === 'object' &&
    'code' in error && error.code === 'PT410' &&
    'message' in error && error.message === 'idempotency_key_expired'
}
