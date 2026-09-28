import { createHmac } from 'node:crypto';

/** Application response header consumed by Gregale's opt-in login-target observer. */
export const PRE_AUTH_TARGET_HEADER = 'X-Gregale-Abuse-Target';

/**
 * Return the opaque target value for a failed login response.
 *
 * The caller must supply the same normalized identifier used for account
 * lookup, including when no account exists. Attach the result only to selected
 * failed responses; this function does not decide whether a login failed.
 * The secret must be an app-owned, randomly generated key shared by replicas.
 */
export function preAuthTargetDigest(secret: string | Uint8Array, normalizedIdentifier: string): string {
  let key: string | Uint8Array;
  if (typeof secret === 'string') {
    if (Buffer.from(secret, 'utf8').toString('utf8') !== secret) {
      throw new TypeError('pre-auth target secret must be valid Unicode');
    }
    key = secret;
  } else if (secret instanceof Uint8Array) {
    key = secret;
  } else {
    throw new TypeError('pre-auth target secret must be a string or Uint8Array');
  }
  if (Buffer.byteLength(key) < 32) {
    throw new TypeError('pre-auth target secret must contain at least 32 bytes');
  }
  if (typeof normalizedIdentifier !== 'string' || !normalizedIdentifier) {
    throw new TypeError('pre-auth target identifier must be a nonempty string');
  }
  if (Buffer.from(normalizedIdentifier, 'utf8').toString('utf8') !== normalizedIdentifier) {
    throw new TypeError('pre-auth target identifier must be valid Unicode');
  }
  return createHmac('sha256', key).update(normalizedIdentifier, 'utf8').digest('hex');
}
