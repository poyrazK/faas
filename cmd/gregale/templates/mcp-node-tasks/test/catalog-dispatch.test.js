import assert from 'node:assert/strict';
import { once } from 'node:events';
import test from 'node:test';
import * as z from 'zod/v4';
import { ResourceTemplate } from '@modelcontextprotocol/server';
import { createApp } from '../app.js';

const names = Object.getOwnPropertyNames(Object.prototype);
const rules = Object.fromEntries(names.map(name => [name, []]));

async function startServer(t, configure, auth = {}) {
  const api = createApp({
    version: 1, endpoint: '/mcp', transport: 'streamable-http', mode: 'stateless', legacy: true,
    allowed_origins: [], tasks: { enabled: false },
    auth: { mode: 'open', tool_scopes: { ...rules, greet: [] }, prompt_scopes: rules,
      resource_scopes: { 'review://names/{name}/{id}': [], 'review://replacement': [], 'customer://records/{recordId}': [] }, ...auth },
  }, { log: () => {} });
  configure(api);
  const server = api.app.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(async () => {
    api.closeTaskSubscriptions();
    await api.handler.close();
    server.closeAllConnections();
    await new Promise(resolve => server.close(resolve));
  });
  const base = `http://127.0.0.1:${server.address().port}`;
  assert.equal((await fetch(base + '/healthz')).status, 200);
  return { api, async rpc(version, method, params = {}) {
    const name = params.name || (method === 'resources/read' ? params.uri : undefined);
    const metadata = version === '2026-07-28' ? {
      'io.modelcontextprotocol/protocolVersion': version,
      'io.modelcontextprotocol/clientInfo': { name: 'catalog-dispatch-test', version: '1' },
      'io.modelcontextprotocol/clientCapabilities': {},
    } : {};
    const response = await fetch(base + '/mcp', {
      method: 'POST', headers: { 'Content-Type': 'application/json', Accept: 'application/json, text/event-stream',
        'MCP-Protocol-Version': version, 'Mcp-Method': method, ...(name ? { 'Mcp-Name': name } : {}) },
      body: JSON.stringify({ jsonrpc: '2.0', id: 1, method, params: { ...params, _meta: metadata } }),
    });
    const text = await response.text();
    assert.equal(response.status, 200, text);
    const data = text.startsWith('{') ? text : text.split('\n').find(line => line.startsWith('data: '))?.slice(6);
    const message = JSON.parse(data);
    assert.equal(message.error, undefined, JSON.stringify(message.error));
    return message.result;
  } };
}

for (const version of ['2026-07-28', '2025-11-25']) {
  test(`prototype property names work through tools, prompts and templates (${version})`, async t => {
    const app = await startServer(t, api => {
      for (const name of names) {
        api.registerTool(name, { inputSchema: z.object({ value: z.string() }) }, async ({ value }) => ({ content: [{ type: 'text', text: name + value }] }));
        api.registerPrompt(name, { argsSchema: z.object({ value: z.string() }) }, ({ value }) => ({ messages: [{ role: 'user', content: { type: 'text', text: name + value } }] }));
        api.registerResource(name, new ResourceTemplate(`review://names/${name}/{id}`, { list: undefined }), {}, async (uri, { id }) => ({ contents: [{ uri: uri.href, text: name + id }] }));
      }
    });
    const tools = (await app.rpc(version, 'tools/list')).tools;
    const prompts = (await app.rpc(version, 'prompts/list')).prompts;
    const templates = (await app.rpc(version, 'resources/templates/list')).resourceTemplates;
    for (const name of names) {
      assert.ok(tools.some(tool => tool.name === name));
      assert.ok(prompts.some(prompt => prompt.name === name));
      assert.ok(templates.some(template => template.name === name));
      assert.equal((await app.rpc(version, 'tools/call', { name, arguments: { value: 'ok' } })).content[0].text, name + 'ok');
      assert.equal((await app.rpc(version, 'prompts/get', { name, arguments: { value: 'ok' } })).messages[0].content.text, name + 'ok');
      assert.equal((await app.rpc(version, 'resources/read', { uri: `review://names/${name}/ok` })).contents[0].text, name + 'ok');
    }
    app.api.replaceTool('constructor', { inputSchema: z.object({}) }, async () => ({ content: [{ type: 'text', text: 'replaced' }] }));
    assert.equal((await app.rpc(version, 'tools/call', { name: 'constructor', arguments: {} })).content[0].text, 'replaced');
    app.api.removeTool('__proto__');
    assert.ok(!(await app.rpc(version, 'tools/list')).tools.some(tool => tool.name === '__proto__'));
    assert.equal(Object.getPrototypeOf({}), Object.prototype);
  });
}

test('an inaccessible prototype-named tool does not break another caller catalog', async t => {
  const app = await startServer(t, api => api.registerTool('constructor', { inputSchema: z.object({}) }, async () => ({ content: [] })), { tool_scopes: { greet: [] } });
  assert.deepEqual((await app.rpc('2026-07-28', 'tools/list')).tools.map(tool => tool.name), ['greet']);
});

test('sample customer resource can be replaced with a different static URI', async t => {
  let called = 0;
  const app = await startServer(t, api => api.replaceResource('customer_record', 'review://replacement', { description: 'Real data' }, async uri => {
    called++;
    return { contents: [{ uri: uri.href, text: 'real data' }] };
  }));
  for (const version of ['2026-07-28', '2025-11-25']) {
    const resources = (await app.rpc(version, 'resources/list')).resources;
    assert.ok(resources.some(resource => resource.name === 'customer_record' && resource.uri === 'review://replacement'));
    assert.ok(!(await app.rpc(version, 'resources/templates/list')).resourceTemplates.some(template => template.name === 'customer_record'));
    assert.equal((await app.rpc(version, 'resources/read', { uri: 'review://replacement' })).contents[0].text, 'real data');
  }
  assert.equal(called, 2);
});

test('replacement customer template uses its own data and completion callback', async t => {
  const app = await startServer(t, api => api.replaceResource('customer_record', new ResourceTemplate('customer://records/{recordId}', {
    list: undefined, complete: { recordId: value => ['new-real-record'].filter(id => id.startsWith(value)) },
  }), {}, async (uri, { recordId }) => ({ contents: [{ uri: uri.href, text: recordId }] })));
  for (const version of ['2026-07-28', '2025-11-25']) {
    assert.equal((await app.rpc(version, 'resources/read', { uri: 'customer://records/new-real-record' })).contents[0].text, 'new-real-record');
    const completion = await app.rpc(version, 'completion/complete', { ref: { type: 'ref/resource', uri: 'customer://records/{recordId}' }, argument: { name: 'recordId', value: 'new-' } });
    assert.deepEqual(completion.completion.values, ['new-real-record']);
  }
});
