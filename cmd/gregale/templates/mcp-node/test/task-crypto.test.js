import test from 'node:test';
import assert from 'node:assert/strict';
import { createMcpTaskPayloadCipher, parseMcpTaskEncryptionKeys } from '../task-crypto.js';

const owner = 'o'.repeat(48);
const a = 'a'.repeat(48);
const b = 'b'.repeat(48);

test('payload key rotation reads legacy and previous versions and authenticates field and key ID', () => {
  const legacy = createMcpTaskPayloadCipher(owner);
  const old = createMcpTaskPayloadCipher(owner, { activeKeyId: 'a', keys: { a } });
  const rotated = createMcpTaskPayloadCipher(owner, { activeKeyId: 'b', keys: { a, b } });
  const plaintext = Buffer.from('private arguments');
  const aad = '["namespace","task-id","arguments"]';
  const original = legacy.encrypt(plaintext, aad);
  const previous = old.encrypt(plaintext, aad);
  const current = rotated.encrypt(plaintext, aad);
  assert.equal(original[0], 1);
  assert.equal(previous[0], 2);
  assert.equal(previous.subarray(2, 3).toString(), 'a');
  assert.equal(current.subarray(2, 3).toString(), 'b');
  for (const payload of [original, previous, current]) assert.deepEqual(rotated.decrypt(payload, aad), plaintext);
  assert.throws(() => old.decrypt(current, aad), /unavailable/);
  assert.throws(() => rotated.decrypt(previous, 'different field'));
  const tampered = Buffer.from(previous);
  tampered[2] = 'b'.charCodeAt(0);
  assert.throws(() => rotated.decrypt(tampered, aad));
  const wrong = createMcpTaskPayloadCipher(owner, { activeKeyId: 'a', keys: { a: b } });
  assert.throws(() => wrong.decrypt(previous, aad));
  assert.ok(!current.includes(plaintext));
});

test('key configuration fails closed without exposing secrets and supports a legacy preparation phase', () => {
  for (const config of [undefined, 'not JSON', {}, { activeKeyId: 'missing', keys: { a } },
    { activeKeyId: 'a', keys: { a: 'short' } }, { activeKeyId: 'legacy', keys: { legacy: a } },
    { activeKeyId: 'a', keys: { a }, unexpected: a }, { activeKeyId: 'bad id', keys: { 'bad id': a } }]) {
    assert.throws(() => parseMcpTaskEncryptionKeys(config), error => !error.message.includes(a));
  }
  const prepared = createMcpTaskPayloadCipher(owner, { activeKeyId: 'legacy', keys: { a } });
  assert.equal(prepared.encrypt(Buffer.from('value'), 'aad')[0], 1);
  assert.throws(() => prepared.decrypt(Buffer.alloc(30), 'aad'), /Invalid/);
});

test('reads retained Go/Python native v2 without changing the explicit Node ring format', () => {
  // Produced by Python AESGCM using the native starter KDF, envelope and AAD.
  const key = 'x'.repeat(32);
  const aad = 'gregale-mcp-task:v1:round2:12345678-1234-4234-8234-123456789abc:result';
  const native = Buffer.from('Agdpbml0aWFsqZTZwvcVEwRe8lO5rMOwyhgZpJu9k00FnpE9HKPm9zeZ5mOkkNmLn2sO', 'base64');
  const legacy = Buffer.from('ASx7QwRFWt5N+HSoLxz0tsVOn53n1/O+pBFGdc9lLjq+2WHqW+Eu/fKJtw==', 'base64');
  const configs = [undefined, { activeKeyId: 'initial', keys: { initial: key } }];
  for (const config of configs) {
    const cipher = createMcpTaskPayloadCipher(key, config, [{ id: 'initial', key }]);
    for (const payload of [native, legacy]) assert.deepEqual(JSON.parse(cipher.decrypt(payload, aad)), { content: [] });
    const written = cipher.encrypt(Buffer.from('{}'), aad);
    assert.equal(written[0], config ? 2 : 1);
    assert.deepEqual(cipher.decrypt(written, aad), Buffer.from('{}'));
    assert.throws(() => cipher.decrypt(native, aad + ':different-field'));
    const tampered = Buffer.from(native); tampered[tampered.length - 1] ^= 1;
    assert.throws(() => cipher.decrypt(tampered, aad));
  }
  assert.throws(() => createMcpTaskPayloadCipher(key).decrypt(native, aad), /unavailable/);
  assert.throws(() => createMcpTaskPayloadCipher('wrong'.repeat(8), undefined, [{ id: 'initial', key: 'wrong'.repeat(8) }]).decrypt(native, aad), /authentication/);
  const rotated = createMcpTaskPayloadCipher('next'.repeat(8), undefined, [{ id: 'next', key: 'next'.repeat(8) }, { id: 'initial', key }]);
  for (const payload of [native, legacy]) assert.deepEqual(JSON.parse(rotated.decrypt(payload, aad)), { content: [] });
});
