// Executed only by the local harness check in a materialized trusted starter.
import test from 'node:test';
import assert from 'node:assert/strict';
import { once } from 'node:events';
import { readFileSync } from 'node:fs';
import { createLocalJWKSet } from 'jose';
import { z } from 'zod';
import { createApp } from './app.js';
import { installJourneyTools } from './journey-tools.mjs';
import { installPrivateJourneyControls } from './journey-private.mjs';

test('private fixture enforces JWT policy and protects real catalog mutation', { timeout: 20000 }, async t => {
  const config = JSON.parse(readFileSync(new URL('./gregale-mcp.json', import.meta.url)));
  const jwks = JSON.parse(readFileSync(new URL('./journey-jwks.json', import.meta.url)));
  const runtime = createApp(config, { keyResolver: createLocalJWKSet(jwks), log: () => {} });
  installJourneyTools(runtime, z);
  installPrivateJourneyControls(runtime, config, createLocalJWKSet(jwks), jwks);
  const listener = runtime.app.listen(0, '127.0.0.1');
  await once(listener, 'listening');
  t.after(async () => { await runtime.handler.close(); await new Promise(resolve => listener.close(resolve)); });
  const origin = `http://127.0.0.1:${listener.address().port}`;
  const token = process.env.GREGALE_JOURNEY_FIXTURE_TOKEN;
  async function rpc(version, credential, method, params = {}) {
    const body = { jsonrpc: '2.0', id: 1, method, params };
    const headers = { 'Content-Type': 'application/json', Accept: 'application/json, text/event-stream', 'MCP-Protocol-Version': version };
    if (credential) headers.Authorization = `Bearer ${credential}`;
    if (version === '2026-07-28') {
      headers['Mcp-Method'] = method;
      if (params.name) headers['Mcp-Name'] = params.name;
      body.params = { ...params, _meta: {
        'io.modelcontextprotocol/protocolVersion': version,
        'io.modelcontextprotocol/clientInfo': { name: 'private-journey-fixture', version: '1' },
        'io.modelcontextprotocol/clientCapabilities': {},
      } };
    }
    const response = await fetch(`${origin}/mcp`, { method: 'POST', headers, body: JSON.stringify(body) });
    const text = await response.text();
    return { status: response.status, text };
  }
  for (const version of ['2026-07-28', '2025-11-25']) {
    for (const invalid of [undefined, 'malformed', process.env.GREGALE_JOURNEY_FIXTURE_EXPIRED, process.env.GREGALE_JOURNEY_FIXTURE_WRONG_AUDIENCE]) {
      assert.equal((await rpc(version, invalid, 'tools/list')).status, 401);
    }
    const names = await rpc(version, token, 'tools/list');
    assert.equal(names.status, 200);
    assert.ok(names.text.includes('journey_echo'));
    assert.equal((await rpc(version, process.env.GREGALE_JOURNEY_FIXTURE_READ_ONLY, 'tools/call', { name: 'journey_echo', arguments: { nonce: 'e2ej-denied' } })).status, 403);
    const called = await rpc(version, token, 'tools/call', { name: 'journey_echo', arguments: { nonce: `e2ej-${version}-reflect` } });
    assert.equal(called.status, 200);
    assert.ok(called.text.includes(token), 'synthetic reflection must reach the native worker redaction boundary');
    const observation = await (await fetch(`${origin}/journey/calls/e2ej-${version}-reflect`)).text();
    assert.equal(JSON.parse(observation).count, 1);
    assert.ok(!observation.includes(token));
  }
  assert.equal((await fetch(`${origin}/journey/calls/e2ej-denied`)).status, 404);
  const before = runtime.catalogStatus();
  assert.equal((await fetch(`${origin}/journey/catalog-drift`, { method: 'POST', headers: { Authorization: `Bearer ${token}` } })).status, 403);
  assert.equal(runtime.catalogStatus().sha256, before.sha256);
  assert.equal((await fetch(`${origin}/journey/catalog-drift`, { method: 'POST', headers: { Authorization: `Bearer ${process.env.GREGALE_JOURNEY_FIXTURE_CONTROL}` } })).status, 204);
  assert.notEqual(runtime.catalogStatus().sha256, before.sha256);
  assert.ok(runtime.catalogStatus().revision > before.revision);
});
