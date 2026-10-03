import test from 'node:test';
import assert from 'node:assert/strict';
import { createHmac } from 'node:crypto';

import {
  DEFAULT_WEBHOOK_TIMESTAMP_TOLERANCE_MS,
  verifyWebhook,
  WEBHOOK_DELIVERY_ID_HEADER,
  WEBHOOK_SIGNATURE_HEADER,
  WEBHOOK_TIMESTAMP_HEADER,
  WebhookVerificationError,
} from '../src/index.js';

const secret = 'whsec_test_123';
const timestamp = 1_712_345_678;
const deliveryId = 'delivery-123';
const body = Buffer.from('{"type":"invoice.paid","amount":42}');
const signature = 'sha256=9733751b9a5946bb55cb0f75a16ae54fa21f3d6e827284736a9a5cdf4b07e6d8';

function headers(
  overrides: Partial<Record<string, string | readonly string[]>> = {},
): Record<string, string | readonly string[]> {
  return {
    [WEBHOOK_SIGNATURE_HEADER]: signature,
    [WEBHOOK_TIMESTAMP_HEADER]: String(timestamp),
    [WEBHOOK_DELIVERY_ID_HEADER]: deliveryId,
    ...overrides,
  };
}

function sign(secretValue: string, unix: number, id: string, rawBody: Uint8Array): string {
  const digest = createHmac('sha256', secretValue)
    .update(`${unix}.${id}.`, 'utf8')
    .update(rawBody)
    .digest('hex');
  return `sha256=${digest}`;
}

function errorCode(code: string) {
  return (error: unknown) => error instanceof WebhookVerificationError && error.code === code;
}

test('verifies Gregale golden signature and returns stable delivery identity', () => {
  const verified = verifyWebhook(secret, headers(), body, { now: timestamp * 1000 });
  assert.deepEqual(verified, { deliveryId, timestamp: new Date(timestamp * 1000) });

  const nodeHeaders = Object.fromEntries(Object.entries(headers()).map(([key, value]) => [key.toLowerCase(), value]));
  assert.equal(verifyWebhook(secret, nodeHeaders, body, { now: timestamp * 1000 }).deliveryId, deliveryId);
});

test('uses the default clock and timestamp tolerance', () => {
  const currentTimestamp = Math.floor(Date.now() / 1000);
  const currentBody = Buffer.from('current event');
  const currentHeaders = headers({
    [WEBHOOK_TIMESTAMP_HEADER]: String(currentTimestamp),
    [WEBHOOK_SIGNATURE_HEADER]: sign(secret, currentTimestamp, deliveryId, currentBody),
  });
  assert.equal(verifyWebhook(secret, currentHeaders, currentBody).deliveryId, deliveryId);
});

test('rejects a changed raw body or wrong secret', () => {
  assert.throws(() => verifyWebhook(secret, headers(), Buffer.concat([body, Buffer.from(' ')]), { now: timestamp * 1000 }), errorCode('bad_signature'));
  assert.throws(() => verifyWebhook('wrong-secret', headers(), body, { now: timestamp * 1000 }), errorCode('bad_signature'));
});

test('rejects stale and future timestamps before signature acceptance', () => {
  const staleBy = DEFAULT_WEBHOOK_TIMESTAMP_TOLERANCE_MS + 1000;
  const stale = timestamp * 1000 + staleBy;
  const staleHeaders = headers({
    [WEBHOOK_TIMESTAMP_HEADER]: String(timestamp),
    [WEBHOOK_SIGNATURE_HEADER]: sign(secret, timestamp, deliveryId, body),
  });
  assert.throws(() => verifyWebhook(secret, staleHeaders, body, { now: stale }), errorCode('stale_timestamp'));

  const futureTimestamp = timestamp + Math.ceil(staleBy / 1000);
  const futureHeaders = headers({
    [WEBHOOK_TIMESTAMP_HEADER]: String(futureTimestamp),
    [WEBHOOK_SIGNATURE_HEADER]: sign(secret, futureTimestamp, deliveryId, body),
  });
  assert.throws(() => verifyWebhook(secret, futureHeaders, body, { now: timestamp * 1000 }), errorCode('stale_timestamp'));
});

test('rejects missing, duplicate, and malformed header values', () => {
  const missing = headers();
  delete missing[WEBHOOK_DELIVERY_ID_HEADER];
  assert.throws(() => verifyWebhook(secret, missing, body, { now: timestamp * 1000 }), errorCode('missing_header'));

  assert.throws(() => verifyWebhook(secret, headers({ [WEBHOOK_SIGNATURE_HEADER]: [signature, signature] }), body, { now: timestamp * 1000 }), errorCode('malformed_header'));
  assert.throws(() => verifyWebhook(secret, headers({ [WEBHOOK_SIGNATURE_HEADER]: 'sha256=not-hex' }), body, { now: timestamp * 1000 }), errorCode('malformed_signature'));
  assert.throws(() => verifyWebhook(secret, headers({ [WEBHOOK_TIMESTAMP_HEADER]: '01712345678' }), body, { now: timestamp * 1000 }), errorCode('malformed_header'));
});

test('rejects empty secrets and negative tolerance', () => {
  assert.throws(() => verifyWebhook('', headers(), body, { now: timestamp * 1000 }), errorCode('invalid_secret'));
  assert.throws(() => verifyWebhook(secret, headers(), body, { timestampToleranceMs: -1 }), errorCode('invalid_options'));
  assert.throws(() => verifyWebhook(secret, headers(), 'body' as unknown as Uint8Array), errorCode('invalid_body'));
});
