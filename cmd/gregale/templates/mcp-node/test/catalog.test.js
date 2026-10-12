import assert from 'node:assert/strict';
import test from 'node:test';
import { createRuntimeCatalog } from '../catalog.js';

test('runtime tool catalog supports register, replace, remove and visible fingerprints', () => {
  const catalog = createRuntimeCatalog();
  const changes = [];
  const unsubscribe = catalog.subscribe(kind => changes.push(kind));
  const handler = async () => ({ content: [{ type: 'text', text: 'ok' }] });

  catalog.registerTool('lookup', { description: 'First', inputSchema: {} }, handler);
  assert.equal(catalog.tools()[0].name, 'lookup');
  assert.equal(catalog.revision(), 1);
  const contract = catalog.contractFingerprint();
  assert.match(contract, /^[a-f0-9]{64}$/);
  const initial = catalog.fingerprint('tools', (_kind, entry) => entry.name === 'lookup');
  catalog.registerTool('private_lookup', { description: 'Private', inputSchema: {} }, handler);
  assert.equal(catalog.revision(), 2);
  assert.equal(catalog.fingerprint('tools', (_kind, entry) => entry.name === 'lookup'), initial);
  catalog.replaceTool('lookup', { description: 'Updated', inputSchema: {} }, handler);
  assert.equal(catalog.revision(), 3);
  assert.notEqual(catalog.fingerprint('tools', (_kind, entry) => entry.name === 'lookup'), initial);
  assert.equal(catalog.removeTool('lookup'), true);
  assert.equal(catalog.revision(), 4);
  assert.equal(catalog.removeTool('lookup'), false);
  assert.deepEqual(changes, ['tools', 'tools', 'tools', 'tools']);
  unsubscribe();
});

test('runtime catalog contract fingerprint ignores registration order', () => {
  const left = createRuntimeCatalog();
  const right = createRuntimeCatalog();
  const handler = async () => ({ content: [] });
  left.registerTool('a', { description: 'A', inputSchema: {} }, handler);
  left.registerTool('b', { description: 'B', inputSchema: {} }, handler);
  right.registerTool('b', { inputSchema: {}, description: 'B' }, handler);
  right.registerTool('a', { inputSchema: {}, description: 'A' }, handler);
  assert.equal(left.contractFingerprint(), right.contractFingerprint());
});

test('runtime resource catalog validates patterns and publishes resource entries', () => {
  const catalog = createRuntimeCatalog();
  const handler = async () => ({ contents: [] });
  const template = { uriTemplate: { toString: () => 'catalog://items/{itemID}' } };
  catalog.registerResource('item', template, { mimeType: 'application/json' }, handler);
  assert.deepEqual(catalog.resourcePatterns(), ['catalog://items/{itemID}']);
  assert.throws(() => catalog.registerResource('bad', 'not a uri', {}, handler), /absolute URI/);
  catalog.replaceResource('item', template, { mimeType: 'text/plain' }, handler);
  assert.equal(catalog.removeResource('item'), true);
});
