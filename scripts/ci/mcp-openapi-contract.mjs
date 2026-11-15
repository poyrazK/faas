// Portable generated-source acceptance. The MCP client/server exchange is real;
// upstream API execution is injected, so CI never calls a third-party service.
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

const project = resolve(process.argv[2]);
const { createApp } = await import(pathToFileURL(resolve(project, 'app.js')));
const { installOpenAPITools, manifest, schemaValidator } = await import(pathToFileURL(resolve(project, 'openapi-tools.js')));
const config = JSON.parse(await readFile(resolve(project, 'gregale-mcp.json'), 'utf8'));
assert.equal(manifest.server_url, 'https://api.example.com/v1');
assert.deepEqual(manifest.operations.map(op => op.id).sort(), ['createReport', 'getReport']);
assert.equal(schemaValidator({ type: 'string', maxLength: 1 }).safeParse('😀').success, true);
assert.equal(schemaValidator({ type: 'string', minLength: 2 }).safeParse('😀').success, false);
assert.equal(schemaValidator({ type: 'string', minLength: 3, enum: ['x', 'valid'] }).safeParse('x').success, false);
assert.equal(schemaValidator({ type: 'string', nullable: true, enum: ['valid'] }).safeParse(null).success, false);

for (const protocol of ['2025-11-25', '2026-07-28']) {
  const calls = [];
  const runtime = createApp(config, { log() {} });
  installOpenAPITools(runtime, { request: async (contract, operation, args) => {
    assert.equal(contract.server_url, manifest.server_url);
    calls.push({ operation: operation.id, args });
    if (args.path?.id === 'bad-shape') return { unreviewed: 'upstream-data-must-not-leak-in-error' };
    return { id: args.path?.id || args.body.name, count: 3 };
  } });
  const listener = runtime.app.listen(0, '127.0.0.1');
  await new Promise(resolve => listener.once('listening', resolve));
  let nextID = 0;
  const rpc = async (method, params, notification = false) => {
    const id = notification ? undefined : ++nextID;
    const modern = protocol === '2026-07-28';
    const bodyParams = modern ? { ...params, _meta: {
      'io.modelcontextprotocol/protocolVersion': protocol,
      'io.modelcontextprotocol/clientInfo': { name: 'openapi-contract', version: '1' },
      'io.modelcontextprotocol/clientCapabilities': {},
    } } : params;
    const response = await fetch('http://127.0.0.1:' + listener.address().port + '/mcp', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json, text/event-stream', 'MCP-Protocol-Version': protocol,
        ...(modern ? { 'Mcp-Method': method, ...(params.name ? { 'Mcp-Name': params.name } : {}) } : {}) },
      body: JSON.stringify({ jsonrpc: '2.0', ...(notification ? {} : { id }), method, params: bodyParams }),
    });
    const text = await response.text();
    assert.ok(response.ok, method + ': ' + response.status + ' ' + text);
    if (notification) return;
    if ((response.headers.get('content-type') || '').includes('application/json')) return JSON.parse(text);
    const messages = text.split(/\r?\n/).filter(line => line.startsWith('data:'))
      .map(line => JSON.parse(line.slice(5).trim()));
    const message = messages.find(value => value.id === id);
    assert.ok(message, 'missing RPC result: ' + text);
    return message;
  };
  try {
    if (protocol === '2025-11-25') {
      const initialized = await rpc('initialize', { protocolVersion: protocol, capabilities: {}, clientInfo: { name: 'openapi-contract', version: '1' } });
      assert.equal(initialized.result.protocolVersion, protocol);
      await rpc('notifications/initialized', {}, true);
    }
    const catalog = await rpc('tools/list', {});
    assert.deepEqual(catalog.result.tools.map(tool => tool.name).sort(), ['createReport', 'getReport']);
    assert.ok(catalog.result.tools.every(tool => tool.inputSchema && tool.outputSchema));
    const report = await rpc('tools/call', { name: 'getReport', arguments: { path: { id: 'report-1' }, query: { verbose: true } } });
    assert.equal(report.error, undefined, JSON.stringify(report));
    assert.deepEqual(report.result.structuredContent, { result: { id: 'report-1', count: 3 } });
    const created = await rpc('tools/call', { name: 'createReport', arguments: { body: { name: 'new-report' } } });
    assert.deepEqual(created.result.structuredContent, { result: { id: 'new-report', count: 3 } });
    const count = calls.length;
    const invalid = await rpc('tools/call', { name: 'getReport', arguments: { path: { id: 'report-1' }, undeclared: true } });
    assert.ok(invalid.error || invalid.result?.isError);
    assert.equal(calls.length, count, 'invalid arguments reached the API');
    const bad = await rpc('tools/call', { name: 'getReport', arguments: { path: { id: 'bad-shape' } } });
    assert.equal(bad.result.isError, true);
    assert.ok(!JSON.stringify(bad).includes('upstream-data-must-not-leak-in-error'));
  } finally {
    listener.closeAllConnections();
    await new Promise(resolve => listener.close(resolve));
    await runtime.handler.close();
  }
  console.log('Generated API tool MCP contract passed: ' + protocol);
}
