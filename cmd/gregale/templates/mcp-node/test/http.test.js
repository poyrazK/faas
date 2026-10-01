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
    for (const file of ['server.js', 'auth.js']) await copyFile(join(source, file), join(fixture, file));
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
