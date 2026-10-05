// Run inside a materialized starter with pinned SDK dependencies installed.
import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { once } from 'node:events';
import { createServer } from 'node:http';
import { promisify } from 'node:util';
import { createApp } from './app.js';

const [cli] = process.argv.slice(2);
assert.ok(cli, 'provide the freshly built CLI');
const run = promisify(execFile), logs = [];
const { app, handler } = createApp({ version: 1, endpoint: '/mcp', transport: 'streamable-http', mode: 'stateless', legacy: true,
  allowed_origins: [], auth: { mode: 'open', tool_scopes: { greet: [], add: [], stream_demo: [] } } }, { log: line => logs.push(line) });
const mcp = createServer((req, res) => {
  assert.equal(req.headers.authorization, undefined, 'operator credentials must never reach the customer endpoint');
  app(req, res);
});
const control = createServer((req, res) => {
  assert.equal(req.url.split('?')[0], '/v1/apps/fixture/logs');
  assert.equal(req.headers.authorization, 'Bearer fixture-operator-token');
  res.setHeader('Content-Type', 'text/event-stream');
  for (const line of ['private-unstructured-log', ...logs]) {
    // Extra log-envelope fields must not be forwarded to the event output.
    res.write(`event: log\ndata: ${JSON.stringify({ line, private_field: 'private-envelope' })}\n\n`);
  }
  res.end('event: end\ndata: {}\n\n');
});
async function listen(server) { server.listen(0, '127.0.0.1'); await once(server, 'listening'); return `http://127.0.0.1:${server.address().port}`; }
try {
  const endpoint = `${await listen(mcp)}/mcp`;
  const env = { ...process.env, FAAS_API: await listen(control), FAAS_TOKEN: 'fixture-operator-token' };
  const requestIDs = [];
  for (const legacy of [false, true]) {
    const args = ['mcp', 'call', '--url', endpoint, '--tool', 'greet', '--arguments', '{"name":"private-argument"}', '--json'];
    if (legacy) args.push('--legacy');
    const { stdout } = await run(cli, args, { env, timeout: 30000 });
    const receipt = JSON.parse(stdout);
    assert.match(receipt.request_id, /^[0-9a-f-]{36}$/);
    requestIDs.push(receipt.request_id);
  }
  async function events(filters = []) {
    const { stdout } = await run(cli, ['mcp', 'events', '--app', 'fixture', '--json', ...filters], { env, timeout: 30000 });
    assert.ok(!stdout.includes('private-') && !stdout.includes('fixture-operator-token'), 'redacted event projection');
    return stdout.trim().split('\n').filter(Boolean).map(line => JSON.parse(line));
  }
  const all = await events(['--tool', 'greet']);
  assert.equal(all.length, 4, 'one callback and one terminal summary per call');
  for (const protocol of ['2026-07-28', '2025-11-25']) {
    const pair = all.filter(event => event.protocol === protocol);
    assert.equal(pair.length, 2);
    assert.deepEqual(new Set(pair.map(event => event.event)), new Set(['mcp_request', 'mcp_tool_call']));
    assert.equal(pair[0].request_id, pair[1].request_id);
    assert.ok(requestIDs.includes(pair[0].request_id), 'call receipt joins the matching execution events');
    assert.ok(pair.every(event => event.outcome === 'success' && event.event_version === 1));
    assert.deepEqual(await events(['--request', pair[0].request_id, '--outcome', 'success']), pair);
  }
  console.log('PASS: official SDK modern/legacy calls -> versioned runtime log envelopes -> filtered, redacted CLI events');
} finally {
  await handler.close();
  await Promise.all([mcp, control].map(server => new Promise(resolve => server.close(resolve))));
}
