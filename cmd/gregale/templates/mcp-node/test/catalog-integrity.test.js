import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { createApp } from '../app.js';

const config = JSON.parse(readFileSync(new URL('../gregale-mcp.json', import.meta.url), 'utf8'));

test('catalog pin accepts a matching contract and blocks later drift', () => {
  const initial = createApp(config, { expectedCatalogSHA256: '' }).catalogStatus();
  assert.equal(initial.ready, true);
  assert.match(initial.sha256, /^[a-f0-9]{64}$/);

  const pinned = createApp(config, { expectedCatalogSHA256: initial.sha256 });
  assert.equal(pinned.catalogStatus().ready, true);
  pinned.registerTool('lookup', { description: 'Lookup', inputSchema: {} }, async () => ({ content: [] }));
  assert.equal(pinned.catalogStatus().ready, false);
});

test('catalog pin rejects invalid digests', () => {
  assert.throws(() => createApp(config, { expectedCatalogSHA256: 'invalid' }), /64-character SHA-256/);
});
