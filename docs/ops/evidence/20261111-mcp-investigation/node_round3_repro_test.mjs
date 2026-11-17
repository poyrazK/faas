// Run against a temporary, dependency-installed copy of the Node starter.
import assert from 'node:assert/strict';
import test from 'node:test';
import { pathToFileURL } from 'node:url';
import { resolve } from 'node:path';
import { readFile } from 'node:fs/promises';
import { once } from 'node:events';

const starter = process.env.MCP_INVESTIGATION_NODE_STARTER;
if (!starter) throw new Error('Set MCP_INVESTIGATION_NODE_STARTER to the dependency-installed Node starter');
const fromStarter = file => import(pathToFileURL(resolve(starter, file)).href);
const { registerMcpTaskHandler, createMcpTaskRuntime } = await fromStarter('tasks.js');
const { createRuntimeCatalog } = await fromStarter('catalog.js');
const { createApp } = await fromStarter('app.js');

test('a registered visible-ASCII task name can construct a runtime', () => {
  const handler = {version: '1', validate() {}, execute: async () => ({content: []})};
  const store = {initialize: async () => {}, claim: async () => null};
  assert.doesNotThrow(() => createMcpTaskRuntime({store, handlers: {lookup: handler}}));
  const registered = registerMcpTaskHandler('LookupRecord', handler);
  assert.doesNotThrow(() => createMcpTaskRuntime({store, handlers: {LookupRecord: registered}}),
    'registration accepts this legal MCP name, but runtime startup rejects it');
});

test('registration owns the nested definition it advertises', () => {
  const catalog = createRuntimeCatalog();
  const definition = {description: 'Example', inputSchema: {}, annotations: {readOnlyHint: true}};
  catalog.registerTool('lookup', definition, async () => ({content: []}));
  const revision = catalog.revision();
  const fingerprint = catalog.fingerprint('tools', () => true);
  const contract = catalog.contractFingerprint();
  let notifications = 0;
  catalog.subscribe(() => { notifications += 1; });
  definition.annotations.readOnlyHint = false;
  assert.equal(catalog.revision(), revision);
  assert.equal(catalog.fingerprint('tools', () => true), fingerprint);
  assert.equal(notifications, 0);
  assert.equal(catalog.contractFingerprint(), contract,
    'advertised definition changed through an external reference, with no revision or list-change notification');
});

test('HTTP discovery silently changes when the original annotation object is edited', async () => {
  const config = JSON.parse(await readFile(resolve(starter, 'gregale-mcp.json'), 'utf8'));
  config.auth.tool_scopes = {...config.auth.tool_scopes, lookup: []};
  const mcp = createApp(config, {expectedCatalogSHA256: '', log() {}});
  const definition = {description: 'Example', inputSchema: {}, annotations: {readOnlyHint: true}};
  mcp.registerTool('lookup', definition, async () => ({content: []}));
  const server = mcp.app.listen(0, '127.0.0.1');
  await once(server, 'listening');
  try {
    const status = mcp.catalogStatus();
    async function listedTool() {
      const response = await fetch(`http://127.0.0.1:${server.address().port}/mcp`, {
        method: 'POST', headers: {'Content-Type': 'application/json',
          Accept: 'application/json, text/event-stream', 'MCP-Protocol-Version': '2026-07-28',
          'Mcp-Method': 'tools/list'},
        body: JSON.stringify({jsonrpc: '2.0', id: 1, method: 'tools/list', params: {_meta: {
          'io.modelcontextprotocol/protocolVersion': '2026-07-28',
          'io.modelcontextprotocol/clientInfo': {name: 'fixture', version: '1'},
          'io.modelcontextprotocol/clientCapabilities': {},
        }}}),
      });
      const body = await response.text();
      assert.equal(response.status, 200, body);
      const data = body.split('\n').find(line => line.startsWith('data: '));
      const envelope = data ? JSON.parse(data.slice(6)) : JSON.parse(body);
      const tool = envelope.result.tools.find(tool => tool.name === 'lookup');
      assert.ok(tool, body);
      return tool;
    }
    assert.equal((await listedTool()).annotations.readOnlyHint, true);
    definition.annotations.readOnlyHint = false;
    assert.equal(mcp.catalogStatus().revision, status.revision);
    assert.equal((await listedTool()).annotations.readOnlyHint, true,
      'HTTP catalog changed without replaceTool or a revision change');
  } finally {
    server.closeAllConnections();
    await new Promise(resolveClose => server.close(resolveClose));
  }
});
