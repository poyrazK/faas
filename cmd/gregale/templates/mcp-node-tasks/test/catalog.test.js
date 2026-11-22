import assert from 'node:assert/strict';
import test from 'node:test';
import { createRuntimeCatalog } from '../catalog.js';
import * as z from 'zod/v4';

test('catalog registration and readers own independent nested metadata', () => {
  for (const kind of ['tool', 'resource', 'prompt']) {
    const catalog = createRuntimeCatalog();
    const definition = { description: 'Original', annotations: { readOnlyHint: true }, _meta: { tags: ['original'] } };
    const handler = async () => ({});
    const register = value => kind === 'resource' ? catalog.registerResource('item', 'item://one', value, handler)
      : kind === 'tool' ? catalog.registerTool('item', value, handler) : catalog.registerPrompt('item', value, handler);
    register(definition);
    const digest = catalog.contractFingerprint();
    const fingerprint = catalog.fingerprint(`${kind}s`, () => true);
    let notifications = 0;
    catalog.subscribe(() => notifications++);
    definition.annotations.readOnlyHint = false;
    definition._meta.tags.push('changed');
    const entry = catalog[`${kind}s`]()[0];
    assert.equal(entry.handler, handler);
    assert.equal(entry.definition.annotations.readOnlyHint, true);
    assert.deepEqual(entry.definition._meta.tags, ['original']);
    entry.definition.annotations.readOnlyHint = false;
    entry.definition._meta.tags.push('reader mutation');
    entry.handler = () => {};
    catalog.fingerprint(`${kind}s`, (_kind, exposed) => { exposed.definition._meta.tags.push('policy mutation'); return true; });
    assert.equal(catalog[`${kind}s`]()[0].handler, handler);
    assert.deepEqual(catalog[`${kind}s`]()[0].definition._meta.tags, ['original']);
    assert.equal(catalog.contractFingerprint(), digest);
    assert.equal(catalog.fingerprint(`${kind}s`, () => true), fingerprint);
    assert.equal(catalog.revision(), 1);
    assert.equal(notifications, 0);
  }
});

test('catalog snapshots preserve SDK validation and reject cyclic metadata before mutation', () => {
  const catalog = createRuntimeCatalog();
  const schema = z.object({ name: z.string().min(1) });
  catalog.registerTool('item', { inputSchema: schema, annotations: { readOnlyHint: true } }, async () => ({}));
  assert.equal(catalog.tools()[0].definition.inputSchema.safeParse({ name: 'ok' }).success, true);
  assert.equal(catalog.tools()[0].definition.inputSchema.safeParse({ name: '' }).success, false);
  const cyclic = {}; cyclic.self = cyclic;
  assert.throws(() => catalog.registerTool('bad', { _meta: cyclic }, async () => ({})), /cycles/);
  assert.equal(catalog.revision(), 1);
  assert.equal(catalog.tools().length, 1);
});

test('schema snapshots isolate nested Zod metadata, readers and registry updates', async () => {
  const catalog = createRuntimeCatalog({ normalizeDefinition: definition => ({
    ...definition, inputSchema: definition.inputSchema['~standard'].jsonSchema.input({ target: 'draft-2020-12' }),
  }) });
  const when = new Date('2026-01-01T00:00:00.000Z');
  const root = { examples: [{ name: 'before', when }] };
  const child = { examples: ['before'] };
  const schema = z.object({ name: z.string().min(1).meta(child) }).meta(root);
  const handler = async () => ({});
  catalog.registerTool('item', { inputSchema: schema }, handler);
  const digest = catalog.contractFingerprint();
  const fingerprint = catalog.fingerprint('tools', () => true);
  let changes = 0;
  catalog.subscribe(() => changes++);
  root.examples[0].name = 'after'; child.examples.push('after');
  when.setUTCFullYear(2030);
  z.globalRegistry.add(schema, { examples: [{ name: 'registry update' }] });
  const reader = catalog.tools()[0].definition.inputSchema;
  const json = reader['~standard'].jsonSchema.input({ target: 'draft-2020-12' });
  assert.deepEqual(json.examples, [{ name: 'before', when: '2026-01-01T00:00:00.000Z' }]);
  assert.deepEqual(json.properties.name.examples, ['before']);
  json.examples[0].name = 'reader mutation'; json.properties.name.examples.push('reader mutation');
  assert.equal(reader.safeParse({ name: '' }).success, false);
  assert.deepEqual(await reader['~standard'].validate({ name: 'ok' }), { value: { name: 'ok' } });
  assert.equal(catalog.contractFingerprint(), digest);
  assert.equal(catalog.fingerprint('tools', () => true), fingerprint);
  assert.equal(catalog.revision(), 1);
  assert.equal(changes, 0);
  catalog.replaceTool('item', { inputSchema: schema }, handler);
  assert.notEqual(catalog.contractFingerprint(), digest);
  assert.equal(changes, 1);
});

test('schema snapshots retain asynchronous transforms and optional prompt completions', async () => {
  const catalog = createRuntimeCatalog();
  const complete = value => ['short', 'simple'].filter(item => item.startsWith(value));
  const field = z.string();
  Object.defineProperty(field, Symbol.for('mcp.completable'), { value: { complete } });
  catalog.registerPrompt('item', { argsSchema: z.object({ style: field.optional(), value: z.string().transform(async value => value.toUpperCase()) }) }, async () => ({}));
  const snapshot = catalog.prompts()[0].definition.argsSchema;
  assert.deepEqual(await snapshot['~standard'].validate({ value: 'hello' }), { value: { value: 'HELLO' } });
  assert.equal(snapshot['~standard'].jsonSchema.input({ target: 'draft-2020-12' }).properties.value.type, 'string');
  assert.deepEqual(snapshot.shape.style[Symbol.for('mcp.completable')].complete('si'), ['simple']);
});

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
