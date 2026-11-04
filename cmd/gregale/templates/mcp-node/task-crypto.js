import { createCipheriv, createDecipheriv, createHash, createHmac, randomBytes } from 'node:crypto';

const KEY_ID = /^[A-Za-z0-9_-]{1,64}$/;

export function parseMcpTaskEncryptionKeys(value) {
  let config;
  try { config = typeof value === 'string' ? JSON.parse(value) : value; }
  catch { throw new Error('Invalid MCP task encryption key configuration'); }
  if (!config || typeof config !== 'object' || Array.isArray(config) ||
      Object.keys(config).some(key => !['activeKeyId', 'keys'].includes(key)) ||
      typeof config.activeKeyId !== 'string' || !KEY_ID.test(config.activeKeyId) ||
      !config.keys || typeof config.keys !== 'object' || Array.isArray(config.keys)) {
    throw new Error('Invalid MCP task encryption key configuration');
  }
  const entries = Object.entries(config.keys);
  if (entries.length < 1 || entries.length > 16 || entries.some(([id, secret]) =>
    !KEY_ID.test(id) || id === 'legacy' || typeof secret !== 'string' || Buffer.byteLength(secret) < 32) ||
    (config.activeKeyId !== 'legacy' && !Object.hasOwn(config.keys, config.activeKeyId))) {
    throw new Error('Invalid MCP task encryption key configuration');
  }
  return { activeKeyId: config.activeKeyId, keys: Object.fromEntries(entries) };
}

export function createMcpTaskPayloadCipher(ownerKey, encryptionKeys, nativeOwnerKeys = [{ id: 'default', key: ownerKey }]) {
  if (typeof ownerKey !== 'string' || Buffer.byteLength(ownerKey) < 32) throw new Error('MCP task owner key must contain at least 32 bytes');
  const keys = new Map([['legacy', createHmac('sha256', ownerKey).update('gregale-mcp-task-payload:v1').digest()]]);
  let activeKeyId = 'legacy';
  if (encryptionKeys !== undefined) {
    const config = parseMcpTaskEncryptionKeys(encryptionKeys);
    activeKeyId = config.activeKeyId;
    for (const [id, secret] of Object.entries(config.keys)) {
      keys.set(id, createHmac('sha256', secret).update(`gregale-mcp-task-payload:v2:${id}`).digest());
    }
  }
  // Go/Python's native v2 envelope carries the key ID in its header but uses
  // the v1 KDF and base AAD. Keep that authenticated format readable during
  // runtime handoffs; Node's explicit ring retains its existing wire format.
  const nativeKeys = new Map();
  for (const { id, key } of nativeOwnerKeys) {
    if (typeof id !== 'string' || !/^[A-Za-z0-9._-]{1,64}$/.test(id) || nativeKeys.has(id) ||
        typeof key !== 'string' || Buffer.byteLength(key) < 32) throw new Error('Invalid MCP task native owner key configuration');
    nativeKeys.set(id, createHmac('sha256', key).update('gregale-mcp-task-payload:v1').digest());
  }
  const fingerprints = new Map([['@owner', createHmac('sha256', ownerKey).update('gregale-mcp-task-identity:v1').digest()]]);
  for (const [id, key] of keys) fingerprints.set(id, createHash('sha256').update(key).digest());

  for (const [id, key] of nativeKeys) fingerprints.set(`@native:${id}`, createHash('sha256').update(key).digest());

  function envelope(value) {
    if (!Buffer.isBuffer(value) || value.length < 30) throw new Error('Invalid encrypted MCP task payload');
    if (value[0] === 1) return { id: 'legacy', offset: 1, version: 1 };
    const length = value[1];
    if (value[0] !== 2 || length < 1 || length > 64 || value.length < 31 + length) throw new Error('Invalid encrypted MCP task payload');
    const id = value.subarray(2, 2 + length).toString('ascii');
    if (!/^[A-Za-z0-9._-]{1,64}$/.test(id) || !Buffer.from(id, 'ascii').equals(value.subarray(2, 2 + length))) throw new Error('Invalid encrypted MCP task payload');
    return { id, offset: 2 + length, version: 2 };
  }

  return {
    fingerprints,
    encrypt(plaintext, aad) {
      const legacy = activeKeyId === 'legacy';
      const id = Buffer.from(activeKeyId, 'ascii');
      const nonce = randomBytes(12);
      const cipher = createCipheriv('aes-256-gcm', keys.get(activeKeyId), nonce);
      cipher.setAAD(Buffer.from(legacy ? aad : `${aad}:key:${activeKeyId}`));
      const encrypted = Buffer.concat([cipher.update(plaintext), cipher.final()]);
      return Buffer.concat([legacy ? Buffer.from([1]) : Buffer.concat([Buffer.from([2, id.length]), id]), nonce, cipher.getAuthTag(), encrypted]);
    },
    decrypt(value, aad) {
      const { id, offset, version } = envelope(value);
      const candidates = [];
      if (keys.has(id)) candidates.push({ key: keys.get(id), aad: version === 1 ? aad : `${aad}:key:${id}` });
      if (version === 2 && nativeKeys.has(id)) candidates.push({ key: nativeKeys.get(id), aad });
      if (version === 1) for (const key of nativeKeys.values()) candidates.push({ key, aad });
      if (!candidates.length) throw new Error('MCP task encryption key is unavailable');
      for (const candidate of candidates) {
        try {
          const decipher = createDecipheriv('aes-256-gcm', candidate.key, value.subarray(offset, offset + 12));
          decipher.setAAD(Buffer.from(candidate.aad));
          decipher.setAuthTag(value.subarray(offset + 12, offset + 28));
          return Buffer.concat([decipher.update(value.subarray(offset + 28)), decipher.final()]);
        } catch { /* A different retained format/key may authenticate this payload. */ }
      }
      throw new Error('MCP task payload authentication failed');
    },
  };
}
