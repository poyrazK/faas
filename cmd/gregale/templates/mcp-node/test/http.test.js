import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, copyFile, readFile, writeFile, rm } from 'node:fs/promises';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { setTimeout as delay } from 'node:timers/promises';

test('HTTP origin policy, tool errors, redacted logs and stream cancellation', { timeout: 15000 }, async () => {
  const source = dirname(dirname(fileURLToPath(import.meta.url)));
  // Keep the fixture below the starter so installed SDK modules remain resolvable.
  const fixture = await mkdtemp(join(source, 'test', '.http-'));
  let child;
  let logs = '';
  try {
    for (const file of ['server.js', 'app.js', 'auth.js', 'tool-policy.js', 'tasks.js', 'task-store.js', 'task-metrics.js', 'task-runtime.js', 'task-limits.json']) await copyFile(join(source, file), join(fixture, file));
    const config = JSON.parse(await readFile(join(source, 'gregale-mcp.json'), 'utf8'));
    config.allowed_origins = ['https://trusted.example'];
    await writeFile(join(fixture, 'gregale-mcp.json'), JSON.stringify(config));
    child = spawn(process.execPath, [join(fixture, 'server.js')], { env: { ...process.env, PORT: '0', MCP_BIND_ADDRESS: '127.0.0.1' }, stdio: ['ignore', 'pipe', 'pipe'] });
    child.stdout.on('data', chunk => { logs += chunk.toString(); });
    child.stderr.on('data', chunk => { logs += chunk.toString(); });
    let port;
    for (let attempt = 0; attempt < 200; attempt++) {
      const line = logs.split('\n').find(line => line.includes('mcp_listening'));
      if (line) { port = JSON.parse(line).port; break; }
      if (child.exitCode !== null) throw new Error('Server exited before readiness');
      await delay(10);
    }
    assert.ok(port, 'server must become ready');
    const endpoint = `http://127.0.0.1:${port}/mcp`;
    function call(name, args, extraHeaders = {}, signal) {
      return fetch(endpoint, { method: 'POST', signal, headers: { 'Content-Type': 'application/json', Accept: 'application/json, text/event-stream', 'MCP-Protocol-Version': '2026-07-28', 'Mcp-Method': 'tools/call', 'Mcp-Name': name, ...extraHeaders }, body: JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'tools/call', params: { name, arguments: args, _meta: { 'io.modelcontextprotocol/protocolVersion': '2026-07-28', 'io.modelcontextprotocol/clientInfo': { name: 'fixture', version: '1' }, 'io.modelcontextprotocol/clientCapabilities': {}, progressToken: 'fixture' } } }) });
    }
    function protocolRequest(method, params = {}) {
      const name = params.name || params.uri;
      const bodyParams = { ...params, _meta: { 'io.modelcontextprotocol/protocolVersion': '2026-07-28', 'io.modelcontextprotocol/clientInfo': { name: 'fixture', version: '1' }, 'io.modelcontextprotocol/clientCapabilities': {} } };
      return fetch(endpoint, { method: 'POST', headers: {
        'Content-Type': 'application/json', Accept: 'application/json, text/event-stream', 'MCP-Protocol-Version': '2026-07-28', 'Mcp-Method': method,
        ...(name ? { 'Mcp-Name': name } : {}),
      }, body: JSON.stringify({ jsonrpc: '2.0', id: 1, method, params: bodyParams }) });
    }
    async function result(response) {
      const text = await response.text();
      const data = text.split('\n').find(line => line.startsWith('data: '));
      assert.ok(data, text);
      return JSON.parse(data.slice(6)).result;
    }
    const denied = await call('greet', { name: 'visitor' }, { Origin: 'https://untrusted.example' });
    assert.equal(denied.status, 403);
    const preflight = await fetch(endpoint, { method: 'OPTIONS', headers: { Origin: 'https://trusted.example', 'Access-Control-Request-Method': 'POST', 'Access-Control-Request-Headers': 'authorization, content-type, mcp-method, mcp-name' } });
    assert.equal(preflight.status, 204);
    assert.equal(preflight.headers.get('Access-Control-Allow-Origin'), 'https://trusted.example');
    const privateInput = 'customer input kept out of logs';
    const greet = await call('greet', { name: privateInput }, { Origin: 'https://trusted.example' });
    assert.equal(greet.status, 200);
    assert.match(await greet.text(), /Hello/);
    assert.ok(!logs.includes(privateInput));
    const resources = await protocolRequest('resources/list');
    assert.equal(resources.status, 200);
    assert.deepEqual((await result(resources)).resources.map(resource => resource.uri), ['greeting://welcome']);
    const templates = await protocolRequest('resources/templates/list');
    assert.deepEqual((await result(templates)).resourceTemplates.map(resource => resource.uriTemplate), ['customer://records/{recordId}']);
    const resource = await protocolRequest('resources/read', { uri: 'customer://records/example-1' });
    assert.equal(resource.status, 200);
    assert.match((await result(resource)).contents[0].text, /example-1/);
    const prompts = await protocolRequest('prompts/list');
    assert.deepEqual((await result(prompts)).prompts.map(prompt => prompt.name), ['summarize']);
    const prompt = await protocolRequest('prompts/get', { name: 'summarize', arguments: { text: 'fixture text' } });
    assert.equal(prompt.status, 200);
    assert.match((await result(prompt)).messages[0].content.text, /fixture text/);
    const discovery = await protocolRequest('server/discover');
    assert.deepEqual((await result(discovery)).capabilities.completions, {});
    const promptCompletion = await protocolRequest('completion/complete', {
      ref: { type: 'ref/prompt', name: 'summarize' }, argument: { name: 'style', value: 'exec' },
    });
    assert.deepEqual((await result(promptCompletion)).completion.values, ['executive']);
    const resourceCompletion = await protocolRequest('completion/complete', {
      ref: { type: 'ref/resource', uri: 'customer://records/{recordId}' }, argument: { name: 'recordId', value: 'example-2' },
    });
    assert.deepEqual((await result(resourceCompletion)).completion.values, ['example-2']);
    const overflow = await call('add', { a: Number.MAX_VALUE, b: Number.MAX_VALUE });
    assert.equal(overflow.status, 200);
    assert.match(await overflow.text(), /"isError":true/);
    const controller = new AbortController();
    const stream = await call('stream_demo', {}, {}, controller.signal);
    const reader = stream.body.getReader();
    let received = '';
    while (!received.includes('notifications/progress')) received += new TextDecoder().decode((await reader.read()).value);
    controller.abort();
    await reader.cancel().catch(() => {});
    for (let attempt = 0; attempt < 100 && !logs.includes('"outcome":"cancelled"'); attempt++) await delay(10);
    assert.match(logs, /"tool":"stream_demo","outcome":"cancelled"/);
    assert.ok(!logs.includes(privateInput));
  } finally {
    if (child && child.exitCode === null) { const exited = once(child, 'exit'); child.kill('SIGTERM'); await exited; }
    await rm(fixture, { recursive: true });
  }
});
