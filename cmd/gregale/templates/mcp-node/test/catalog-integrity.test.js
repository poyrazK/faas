import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { createApp } from '../app.js';
import * as z from 'zod/v4';

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

test('nested caller metadata cannot drift a pinned catalog; explicit replacement does', async () => {
  const definition = { description: 'Lookup', inputSchema: z.object({ id: z.string() }), annotations: { readOnlyHint: true }, _meta: { tags: ['stable'] } };
  const initial = createApp(config, { expectedCatalogSHA256: '' });
  initial.registerTool('lookup', definition, async () => ({ content: [] }));
  const pinned = createApp(config, { expectedCatalogSHA256: initial.catalogStatus().sha256 });
  pinned.registerTool('lookup', definition, async () => ({ content: [] }));
  try {
    const before = pinned.catalogStatus();
    definition.annotations.readOnlyHint = false;
    definition._meta.tags.push('changed');
    assert.deepEqual(pinned.catalogStatus(), before);
    pinned.replaceTool('lookup', definition, async () => ({ content: [] }));
    assert.equal(pinned.catalogStatus().ready, false);
    assert.ok(pinned.catalogStatus().revision > before.revision);
  } finally { await Promise.all([initial.handler.close(), pinned.handler.close()]); }
});

test('Zod metadata mutations leave matching catalog pins ready until explicit replacement', async () => {
  for (const key of ['inputSchema', 'outputSchema', 'argsSchema']) {
    const metadata = { examples: [{ id: 'before' }] };
    const childMetadata = { examples: ['before'] };
    const definition = { description: 'Lookup', [key]: z.object({ id: z.string().meta(childMetadata) }).meta(metadata) };
    const initial = createApp(config, { expectedCatalogSHA256: '' });
    const register = app => key === 'argsSchema' ? app.registerPrompt.bind(app) : app.registerTool.bind(app);
    const replace = app => key === 'argsSchema' ? app.replacePrompt.bind(app) : app.replaceTool.bind(app);
    register(initial)('lookup', definition, async () => ({ content: [] }));
    const pinned = createApp(config, { expectedCatalogSHA256: initial.catalogStatus().sha256 });
    register(pinned)('lookup', definition, async () => ({ content: [] }));
    try {
      const before = pinned.catalogStatus();
      assert.equal(before.ready, true);
      metadata.examples[0].id = 'after'; childMetadata.examples.push('after');
      assert.deepEqual(pinned.catalogStatus(), before, key);
      replace(pinned)('lookup', definition, async () => ({ content: [] }));
      assert.equal(pinned.catalogStatus().ready, false);
      assert.equal(pinned.catalogStatus().revision, before.revision + 1);
    } finally { await Promise.all([initial.handler.close(), pinned.handler.close()]); }
  }
});
