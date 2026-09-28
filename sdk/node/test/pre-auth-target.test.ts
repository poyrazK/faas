import test from 'node:test';
import assert from 'node:assert/strict';

import { preAuthTargetDigest, PRE_AUTH_TARGET_HEADER } from '../src/index.js';

test('produces the lowercase HMAC-SHA256 target expected by the gateway', () => {
  assert.equal(PRE_AUTH_TARGET_HEADER, 'X-Gregale-Abuse-Target');
  assert.equal(
    preAuthTargetDigest(new Uint8Array(32).fill(0x0b), 'Hi There'),
    '198a607eb44bfbc69903a0f1cf2bbdc5ba0aa3f3d9ae3c1c7a3b1696a0b68cf7',
  );
  assert.equal(
    preAuthTargetDigest('a'.repeat(32), 'é@example.com'),
    'b0759e2dbe2de6f20fca64ac31d0dcd0828401f3d62ae4146267607dd77cb52a',
  );
});

test('preserves caller normalization and does not expose inputs in errors', () => {
  const secret = 'private-app-owned-secret-that-is-long-enough';
  assert.notEqual(preAuthTargetDigest(secret, 'user@example.com'), preAuthTargetDigest(secret, 'User@example.com'));
  assert.notEqual(preAuthTargetDigest(secret, 'user@example.com'), preAuthTargetDigest(secret, ' user@example.com '));
  assert.throws(() => preAuthTargetDigest('short', 'user@example.com'), (error: unknown) => {
    assert.ok(error instanceof TypeError);
    assert.match(error.message, /at least 32 bytes/);
    assert.ok(!error.message.includes('short'));
    assert.ok(!error.message.includes('user@example.com'));
    return true;
  });
  assert.throws(() => preAuthTargetDigest(secret, ''), /nonempty string/);
  assert.throws(() => preAuthTargetDigest(secret, '\ud800'), /valid Unicode/);
});
