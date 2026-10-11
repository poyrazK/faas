import test from 'node:test';
import assert from 'node:assert/strict';
import { once } from 'node:events';
import { generateKeyPair, SignJWT } from 'jose';
import { createApp } from '../app.js';

const config = {
  version: 1, endpoint: '/mcp', transport: 'streamable-http', mode: 'stateless', legacy: true, allowed_origins: [],
  auth: {
    mode: 'external-oauth', issuer: 'https://issuer.example', jwks_url: 'https://issuer.example/jwks',
    resource: 'https://mcp.example/mcp', scopes: ['mcp:tools'],
    tool_scopes: { greet: [], add: ['math:read', 'math:write'] },
    resource_scopes: { 'greeting://welcome': [], 'customer://records/{recordId}': ['records:read'] },
    prompt_scopes: { summarize: ['reports:read'] },
  },
};
const { publicKey, privateKey } = await generateKeyPair('RS256');
async function token(scope, subject = 'private-customer-identity') {
  return new SignJWT({ scope }).setProtectedHeader({ alg: 'RS256' }).setAudience(config.auth.resource)
    .setIssuer(config.auth.issuer).setSubject(subject).setExpirationTime('5m').sign(privateKey);
}
async function serve(t, cfg = config) {
  const logs = [];
  const { app, handler } = createApp(cfg, { keyResolver: publicKey, log: line => logs.push(JSON.parse(line)) });
  const listener = app.listen(0, '127.0.0.1');
  await once(listener, 'listening');
  t.after(async () => { await handler.close(); await new Promise(resolve => listener.close(resolve)); });
  const endpoint = `http://127.0.0.1:${listener.address().port}/mcp`;
  return { endpoint, logs };
}
function envelope(method, params, version) {
  const body = { jsonrpc: '2.0', id: 1, method, params };
  if (version === '2026-07-28') body.params = {
    ...params, _meta: {
      'io.modelcontextprotocol/protocolVersion': version,
      'io.modelcontextprotocol/clientInfo': { name: 'authorization-fixture', version: '1' },
      'io.modelcontextprotocol/clientCapabilities': {},
    },
  };
  return body;
}
async function request(endpoint, version, credential, method, params = {}, extraHeaders = {}) {
  const headers = { 'Content-Type': 'application/json', Accept: 'application/json, text/event-stream', 'MCP-Protocol-Version': version };
  if (version === '2026-07-28') {
    headers['Mcp-Method'] = method;
    if (params.name || params.uri) headers['Mcp-Name'] = params.name || params.uri;
  }
  if (credential) headers.Authorization = `Bearer ${credential}`;
  const response = await fetch(endpoint, { method: 'POST', headers: { ...headers, ...extraHeaders }, body: JSON.stringify(envelope(method, params, version)) });
  const text = await response.text();
  const messages = response.headers.get('content-type')?.includes('text/event-stream')
    ? text.split('\n').filter(line => line.startsWith('data: ')).map(line => JSON.parse(line.slice(6)))
    : [JSON.parse(text)];
  return { response, messages, text };
}
async function tools(endpoint, version, credential) {
  return (await catalog(endpoint, version, credential, 'tools/list', 'tools')).map(tool => tool.name);
}
async function catalog(endpoint, version, credential, method, field) {
  const { response, messages, text } = await request(endpoint, version, credential, method);
  assert.equal(response.status, 200, text);
  return messages.find(message => message.id === 1).result[field];
}

for (const version of ['2026-07-28', '2025-11-25']) test(`${version}: caller-specific discovery and independent execution policy`, { timeout: 10000 }, async t => {
  const { endpoint, logs } = await serve(t);
  const reader = await token('mcp:tools math:read');
  const writer = await token('mcp:tools math:read math:write records:read reports:read', 'demo-caller-a');
  if (version === '2025-11-25') {
    const init = await request(endpoint, version, reader, 'initialize', { protocolVersion: version, clientInfo: { name: 'fixture', version: '1' }, capabilities: {} });
    assert.equal(init.response.status, 200);
  }
  assert.deepEqual(await tools(endpoint, version, reader), ['greet']);
  assert.deepEqual(await tools(endpoint, version, writer), ['greet', 'add']);
  // Alternating/concurrent callers must never share a cached permission set.
  const catalogs = await Promise.all(Array.from({ length: 8 }, (_, i) => tools(endpoint, version, i % 2 ? writer : reader)));
  catalogs.forEach((catalog, i) => assert.deepEqual(catalog, i % 2 ? ['greet', 'add'] : ['greet']));
  assert.deepEqual((await catalog(endpoint, version, reader, 'resources/list', 'resources')).map(resource => resource.uri), ['greeting://welcome']);
  assert.deepEqual((await catalog(endpoint, version, reader, 'resources/templates/list', 'resourceTemplates')), []);
  assert.deepEqual((await catalog(endpoint, version, writer, 'resources/templates/list', 'resourceTemplates')).map(resource => resource.uriTemplate), ['customer://records/{recordId}']);
  assert.deepEqual((await catalog(endpoint, version, reader, 'prompts/list', 'prompts')).map(prompt => prompt.name), []);
  assert.deepEqual((await catalog(endpoint, version, writer, 'prompts/list', 'prompts')).map(prompt => prompt.name), ['summarize']);

  const welcome = await request(endpoint, version, reader, 'resources/read', { uri: 'greeting://welcome' });
  assert.equal(welcome.response.status, 200, welcome.text);
  assert.match(welcome.text, /Welcome to the Gregale MCP starter/);
  const hiddenResource = await request(endpoint, version, reader, 'resources/read', { uri: 'customer://records/example-1' }, { 'Mcp-Name': 'welcome', 'Mcp-Method': 'prompts/list' });
  assert.equal(hiddenResource.response.status, 403);
  assert.match(hiddenResource.response.headers.get('www-authenticate'), /scope="mcp:tools records:read"/);
  assert.ok(!hiddenResource.text.includes('example-1'));
  const visibleResource = await request(endpoint, version, writer, 'resources/read', { uri: 'customer://records/example-1' });
  assert.equal(visibleResource.response.status, 200);
  assert.match(visibleResource.messages.find(message => message.id === 1).result.contents[0].text, /"recordId":"example-1"/);

  const hiddenPrompt = await request(endpoint, version, reader, 'prompts/get', { name: 'summarize', arguments: { text: 'private input' } });
  assert.equal(hiddenPrompt.response.status, 403);
  assert.match(hiddenPrompt.response.headers.get('www-authenticate'), /scope="mcp:tools reports:read"/);
  assert.ok(!hiddenPrompt.text.includes('private input'));
  const visiblePrompt = await request(endpoint, version, writer, 'prompts/get', { name: 'summarize', arguments: { text: 'public input' } });
  assert.equal(visiblePrompt.response.status, 200);
  assert.match(visiblePrompt.text, /public input/);
  for (const [name, arguments_, headers] of [
    ['add', { a: 7, b: 5 }, {}],
    ['add', { a: 7, b: 5, scope: 'math:write', subject: 'admin' }, { 'Mcp-Name': 'greet', 'Mcp-Method': 'tools/list', 'X-Scopes': 'math:write' }],
    ['stream_demo', {}, {}],
    ['toString', {}, {}],
  ]) {
    const denied = await request(endpoint, version, reader, 'tools/call', { name, arguments: arguments_ }, headers);
    assert.equal(denied.response.status, 403);
    assert.equal(logs.length, 0, 'denied tool callback must never execute');
    if (name === 'add') {
      assert.match(denied.response.headers.get('www-authenticate'), /error="insufficient_scope"/);
      assert.match(denied.response.headers.get('www-authenticate'), /scope="mcp:tools math:read math:write"/);
      assert.match(denied.response.headers.get('www-authenticate'), /resource_metadata=/);
    }
    assert.ok(!denied.text.includes(reader));
  }
  const greet = await request(endpoint, version, reader, 'tools/call', { name: 'greet', arguments: { name: 'private argument' } });
  assert.equal(greet.response.status, 200);
  assert.match(greet.text, /Hello/);
  const add = await request(endpoint, version, writer, 'tools/call', { name: 'add', arguments: { a: 7, b: 5 } });
  assert.equal(add.response.status, 200);
  assert.match(add.text, /"sum":12/);
  assert.deepEqual(logs.map(log => log.tool), ['greet', 'add']);
  for (const value of [reader, writer, 'private argument', 'private-customer-identity']) assert.ok(!JSON.stringify(logs).includes(value));
  for (const credential of [undefined, 'forged-token', await token('math:read math:write')]) {
    const denied = await request(endpoint, version, credential, 'tools/call', { name: 'add', arguments: { a: 1, b: 2 } });
    assert.equal(denied.response.status, credential?.includes('.') ? 403 : 401);
    assert.equal(logs.length, 2);
  }
});
test('empty policy produces an empty catalog and rejects all execution', async t => {
  const { endpoint, logs } = await serve(t, { ...config, auth: { ...config.auth, tool_scopes: {}, resource_scopes: {}, prompt_scopes: {} } });
  const credential = await token('mcp:tools math:read math:write records:read reports:read');
  assert.deepEqual(await tools(endpoint, '2026-07-28', credential), []);
  assert.deepEqual(await catalog(endpoint, '2026-07-28', credential, 'resources/list', 'resources'), []);
  assert.deepEqual(await catalog(endpoint, '2026-07-28', credential, 'resources/templates/list', 'resourceTemplates'), []);
  assert.deepEqual(await catalog(endpoint, '2026-07-28', credential, 'prompts/list', 'prompts'), []);
  assert.equal((await request(endpoint, '2026-07-28', credential, 'tools/call', { name: 'greet', arguments: { name: 'visitor' } })).response.status, 403);
  assert.equal((await request(endpoint, '2026-07-28', credential, 'resources/read', { uri: 'greeting://welcome' })).response.status, 403);
  assert.equal((await request(endpoint, '2026-07-28', credential, 'prompts/get', { name: 'summarize', arguments: { text: 'visitor' } })).response.status, 403);
  assert.equal(logs.length, 0);
});
test('legacy batched requests cannot bypass disabled tool registration', async t => {
  const { endpoint, logs } = await serve(t);
  const credential = await token('mcp:tools');
  const response = await fetch(endpoint, {
    method: 'POST', headers: { 'Content-Type': 'application/json', Accept: 'application/json, text/event-stream', 'MCP-Protocol-Version': '2025-11-25', Authorization: `Bearer ${credential}` },
    body: JSON.stringify([envelope('tools/call', { name: 'add', arguments: { a: 1, b: 2 } }, '2025-11-25')]),
  });
  assert.match(await response.text(), /"error"/);
  assert.equal(logs.length, 0);
});
