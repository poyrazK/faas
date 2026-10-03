import { createHmac, timingSafeEqual } from 'node:crypto';

export const WEBHOOK_SIGNATURE_HEADER = 'X-Faas-Webhook-Signature';
export const WEBHOOK_TIMESTAMP_HEADER = 'X-Faas-Webhook-Timestamp';
export const WEBHOOK_DELIVERY_ID_HEADER = 'X-Faas-Delivery-Id';
export const DEFAULT_WEBHOOK_TIMESTAMP_TOLERANCE_MS = 5 * 60 * 1000;

export type WebhookVerificationErrorCode =
  | 'missing_header'
  | 'malformed_header'
  | 'malformed_signature'
  | 'stale_timestamp'
  | 'bad_signature'
  | 'invalid_secret'
  | 'invalid_body'
  | 'invalid_options';

/** A safe-to-log verification failure. Messages never include the secret, signature, or body. */
export class WebhookVerificationError extends Error {
  constructor(readonly code: WebhookVerificationErrorCode) {
    super(`webhook verification: ${code}`);
    this.name = 'WebhookVerificationError';
  }
}

/** Authenticated metadata for one outbound Gregale webhook delivery. */
export interface VerifiedWebhook {
  /** Stable across automatic retries; persist it as the receiver's idempotency key. */
  deliveryId: string;
  /** Timestamp included in the signature and checked against the replay window. */
  timestamp: Date;
}

export type WebhookHeaders = Headers | Readonly<Record<string, string | readonly string[] | undefined>>;

export interface VerifyWebhookOptions {
  /** Accepted clock skew in milliseconds. Defaults to five minutes. */
  timestampToleranceMs?: number;
  /** Primarily useful for tests; applications normally leave this unset. */
  now?: Date | number;
}

/**
 * Verify a Gregale outbound webhook using its raw body and request headers.
 * Pass the exact bytes received, before parsing or re-serializing JSON. The
 * signature is HMAC-SHA256 over `<unix_timestamp>.<delivery_id>.<raw_body>`.
 * Persist the returned deliveryId with the handler's side effects to
 * deduplicate retries; signature verification alone does not prevent a replay
 * within the accepted timestamp window.
 */
export function verifyWebhook(
  secret: string | Uint8Array,
  headers: WebhookHeaders,
  body: Uint8Array,
  options: VerifyWebhookOptions = {},
): VerifiedWebhook {
  const key = typeof secret === 'string' ? Buffer.from(secret, 'utf8') : Buffer.from(secret);
  if (key.byteLength === 0) throw new WebhookVerificationError('invalid_secret');
  if (!(body instanceof Uint8Array)) throw new WebhookVerificationError('invalid_body');

  const tolerance = options.timestampToleranceMs ?? DEFAULT_WEBHOOK_TIMESTAMP_TOLERANCE_MS;
  if (!Number.isFinite(tolerance) || tolerance < 0) throw new WebhookVerificationError('invalid_options');
  const effectiveTolerance = tolerance === 0 ? DEFAULT_WEBHOOK_TIMESTAMP_TOLERANCE_MS : tolerance;
  const now = options.now instanceof Date ? options.now.getTime() : options.now ?? Date.now();
  if (!Number.isFinite(now)) throw new WebhookVerificationError('invalid_options');

  const signatureHeader = readSingleHeader(headers, WEBHOOK_SIGNATURE_HEADER);
  const timestampHeader = readSingleHeader(headers, WEBHOOK_TIMESTAMP_HEADER);
  const deliveryId = readSingleHeader(headers, WEBHOOK_DELIVERY_ID_HEADER);
  if (deliveryId.trim() !== deliveryId || /[,\r\n]/.test(deliveryId) || Buffer.byteLength(deliveryId, 'utf8') > 256) {
    throw new WebhookVerificationError('malformed_header');
  }

  if (!/^(0|[1-9][0-9]*)$/.test(timestampHeader)) throw new WebhookVerificationError('malformed_header');
  const timestampSeconds = Number(timestampHeader);
  const timestampMs = timestampSeconds * 1000;
  if (!Number.isSafeInteger(timestampSeconds) || !Number.isSafeInteger(timestampMs)) {
    throw new WebhookVerificationError('malformed_header');
  }
  if (Math.abs(now - timestampMs) > effectiveTolerance) throw new WebhookVerificationError('stale_timestamp');

  const match = /^sha256=([0-9a-fA-F]{64})$/.exec(signatureHeader);
  if (!match?.[1]) throw new WebhookVerificationError('malformed_signature');
  const supplied = Buffer.from(match[1], 'hex');
  const expected = createHmac('sha256', key)
    .update(`${timestampSeconds}.${deliveryId}.`, 'utf8')
    .update(body)
    .digest();
  if (supplied.byteLength !== expected.byteLength || !timingSafeEqual(supplied, expected)) {
    throw new WebhookVerificationError('bad_signature');
  }

  return { deliveryId, timestamp: new Date(timestampMs) };
}

function readSingleHeader(headers: WebhookHeaders, name: string): string {
  const values: string[] = [];
  let found = false;
  if (headers instanceof Headers) {
    headers.forEach((value, key) => {
      if (key.toLowerCase() === name.toLowerCase()) {
        found = true;
        values.push(value);
      }
    });
  } else {
    for (const [key, value] of Object.entries(headers)) {
      if (key.toLowerCase() !== name.toLowerCase() || value === undefined) continue;
      found = true;
      if (typeof value === 'string') values.push(value);
      else values.push(...value);
    }
  }
  if (!found) throw new WebhookVerificationError('missing_header');
  if (values.length !== 1 || values[0] === '') throw new WebhookVerificationError('malformed_header');
  return values[0] ?? '';
}
