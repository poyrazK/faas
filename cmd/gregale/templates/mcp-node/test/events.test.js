import test from 'node:test';
import assert from 'node:assert/strict';
import { once } from 'node:events';
import { setTimeout as delay } from 'node:timers/promises';
import { generateKeyPair, SignJWT } from 'jose';
import * as z from 'zod/v4';
import express from 'express';
import { toNodeHandler } from '@modelcontextprotocol/node';
import { createMcpHandler, McpServer } from '@modelcontextprotocol/server';
import { createApp } from '../app.js';
import { createEvents, requestIDHeader } from '../events.js';

const config = { version: 1, endpoint: '/mcp', transport: 'streamable-http', mode: 'stateless', legacy: true,
  allowed_origins: ['https://trusted.example'], auth: { mode: 'open', tool_scopes: { greet: [], add: [], stream_demo: [] } } };
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
async function serve(t, profile = config, options = {}) {
  const logs = [];
  const { app, handler } = createApp(profile, { log: line => logs.push(JSON.parse(line)), ...options });
  const listener = app.listen(0, '127.0.0.1');
  await once(listener, 'listening');
  t.after(async () => { await handler.close(); await new Promise(resolve => listener.close(resolve)); });
  return { endpoint: `http://127.0.0.1:${listener.address().port}/mcp`, logs };
}
function call(endpoint, version, name, args, extra = {}, signal) {
  const meta = { progressToken: 'private-progress-token', ...(version === '2026-07-28' ? { 'io.modelcontextprotocol/protocolVersion': version,
    'io.modelcontextprotocol/clientInfo': { name: 'private-client', version: 'private-version' },
    'io.modelcontextprotocol/clientCapabilities': {} } : {}) };
  return fetch(endpoint, { method: 'POST', signal, headers: { 'Content-Type': 'application/json', Accept: 'application/json, text/event-stream',
    'MCP-Protocol-Version': version, ...(version === '2026-07-28' ? { 'Mcp-Method': 'tools/call', 'Mcp-Name': name } : {}),
    'X-MCP-Request-ID': 'private-forged-id', 'X-Faas-Request-Id': 'private-gateway-id', ...extra },
  body: JSON.stringify({ jsonrpc: '2.0', id: 'private-jsonrpc-id', method: 'tools/call', params: { name, arguments: args, _meta: meta } }) });
}
function requestEvent(logs, response) {
  const id = response.headers.get(requestIDHeader);
  assert.match(id, uuid);
  const events = logs.filter(event => event.request_id === id);
  const requests = events.filter(event => event.event === 'mcp_request');
  assert.equal(requests.length, 1, 'one terminal request summary');
  return { request: requests[0], callbacks: events.filter(event => event.event === 'mcp_tool_call') };
}
for (const version of ['2026-07-28', '2025-11-25']) test(`correlated, redacted execution and validation: ${version}`, async t => {
  const { endpoint, logs } = await serve(t);
  const replies = await Promise.all(Array.from({ length: 6 }, () => call(endpoint, version, 'greet', { name: 'private-argument' }, { Origin: 'https://trusted.example' })));
  for (const response of replies) {
    assert.match(await response.text(), /Hello/);
    assert.match(response.headers.get('access-control-expose-headers'), /X-MCP-Request-ID/);
    const { request, callbacks } = requestEvent(logs, response);
    assert.equal(request.outcome, 'success');
    assert.equal(request.http_status, 200);
    assert.equal(request.protocol, version);
    assert.equal(request.rpc_method, 'tools/call');
    assert.equal(callbacks.length, 1);
    assert.equal(callbacks[0].tool, 'greet');
    assert.equal(callbacks[0].outcome, 'success');
  }
  assert.equal(new Set(replies.map(response => response.headers.get(requestIDHeader))).size, replies.length);
  const invalid = await call(endpoint, version, 'greet', { name: { secret: 'private-validation-value' } });
  assert.match(await invalid.text(), /"isError":true/);
  const failed = requestEvent(logs, invalid);
  assert.equal(failed.request.outcome, 'validation_error');
  assert.equal(failed.request.reason, 'input_validation');
  assert.equal(failed.callbacks.length, 0);
  const overflow = await call(endpoint, version, 'add', { a: Number.MAX_VALUE, b: Number.MAX_VALUE });
  assert.match(await overflow.text(), /"isError":true/);
  assert.equal(requestEvent(logs, overflow).request.outcome, 'tool_error');
  const denied = await call(endpoint, version, 'private-unknown-tool', {});
  assert.equal(denied.status, 403);
  await denied.text();
  assert.equal(requestEvent(logs, denied).request.outcome, 'denied');
  assert.equal(requestEvent(logs, denied).request.tool, 'unknown');
  assert.equal(requestEvent(logs, denied).callbacks.length, 0);
  const origin = await call(endpoint, version, 'greet', {}, { Origin: 'https://private-origin.example' });
  await origin.text();
  assert.equal(requestEvent(logs, origin).request.reason, 'origin_not_allowed');
  const malformed = await fetch(endpoint, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{"private-malformed-body":' });
  assert.equal(malformed.status, 400);
  await malformed.text();
  assert.equal(requestEvent(logs, malformed).request.reason, 'invalid_body');
  for (const event of logs) {
    assert.equal(event.event_version, 1);
    assert.ok(Number.isInteger(event.duration_ms) && event.duration_ms >= 0);
  }
  assert.ok(!JSON.stringify(logs).includes('private-'), 'no caller data enters events');
});
test('authentication denials omit tokens, subjects and unparsed body fields', async t => {
  const { publicKey, privateKey } = await generateKeyPair('RS256');
  const profile = { ...config, auth: { mode: 'external-oauth', issuer: 'https://issuer.example', jwks_url: 'https://issuer.example/jwks',
    resource: 'https://mcp.example/mcp', scopes: ['mcp:tools'], tool_scopes: { greet: [], add: ['math:write'] } } };
  const credential = await new SignJWT({ scope: 'mcp:tools' }).setProtectedHeader({ alg: 'RS256' }).setIssuer(profile.auth.issuer)
    .setAudience(profile.auth.resource).setSubject('private-subject').setExpirationTime('5m').sign(privateKey);
  const { endpoint, logs } = await serve(t, profile, { keyResolver: publicKey });
  for (const [authorization, reason] of [[undefined, 'authentication_required'], ['Bearer private-bad-token', 'invalid_token'], [`Bearer ${credential}`, 'insufficient_scope']]) {
    const response = await call(endpoint, '2026-07-28', 'add', { a: 1, b: 2 }, authorization ? { Authorization: authorization } : {});
    await response.text();
    const { request, callbacks } = requestEvent(logs, response);
    assert.equal(request.outcome, 'denied');
    assert.equal(request.reason, reason);
    assert.equal(callbacks.length, 0);
  }
  assert.ok(!JSON.stringify(logs).includes(credential));
  assert.ok(!JSON.stringify(logs).includes('private-'));
});
for (const version of ['2026-07-28', '2025-11-25']) test(`disconnect emits one correlated cancellation: ${version}`, async t => {
  const { endpoint, logs } = await serve(t);
  const controller = new AbortController();
  const response = await call(endpoint, version, 'stream_demo', {}, {}, controller.signal);
  const reader = response.body.getReader();
  let received = '';
  while (!received.includes('notifications/progress')) received += new TextDecoder().decode((await reader.read()).value);
  controller.abort();
  await reader.cancel().catch(() => {});
  for (let i = 0; i < 100 && !logs.some(e => e.event === 'mcp_tool_call' && e.outcome === 'cancelled'); i++) await delay(10);
  const { request, callbacks } = requestEvent(logs, response);
  assert.equal(request.outcome, 'cancelled');
  assert.equal(callbacks.length, 1);
  assert.equal(callbacks[0].outcome, 'cancelled');
});
test('public schema observation preserves discovery; errors and output validation stay redacted', async () => {
  const logs = [], events = createEvents(line => logs.push(JSON.parse(line)));
  const state = { id: 'b9ddc41d-60c6-4f22-8d16-f6c3248c0453', protocol: '2026-07-28', method: 'tools/call' };
  const original = z.object({ sum: z.number().finite() });
  const observed = events.schema(original, 'output', state);
  assert.deepEqual(observed['~standard'].jsonSchema.output({ target: 'draft-2020-12' }), original['~standard'].jsonSchema.output({ target: 'draft-2020-12' }));
  await observed['~standard'].validate({ sum: 'private-output' });
  assert.equal(state.reason, 'output_validation');
  await assert.rejects(events.observe('add', async () => { throw new Error('private-exception'); }, state)({}, { mcpReq: { signal: new AbortController().signal } }));
  assert.equal(logs[0].outcome, 'error');
  assert.ok(!JSON.stringify(logs).includes('private-'));
});
test('failed logging cannot break successful tool execution', async t => {
  const { endpoint } = await serve(t, config, { log: () => { throw new Error('logger failed'); } });
  const response = await call(endpoint, '2026-07-28', 'greet', { name: 'visitor' });
  assert.equal(response.status, 200);
  assert.match(await response.text(), /Hello/);
});
for (const [label, callback, expected] of [
  ['invalid output', async () => ({ content: [], structuredContent: { sum: 'private-invalid-output' } }), 'validation_error'],
  ['missing output', async () => ({ content: [] }), 'validation_error'],
  ['exception', async () => { throw new Error('private-callback-exception'); }, 'error'],
]) test(`SDK terminal request observes ${label} after dispatch`, async t => {
  const logs = [], events = createEvents(line => logs.push(JSON.parse(line)));
  events.register('add');
  const handler = createMcpHandler(() => {
    const server = new McpServer({ name: 'event-fixture', version: '1' });
    server.registerTool('add', { inputSchema: events.schema(z.object({}), 'input'),
      outputSchema: events.schema(z.object({ sum: z.number() }), 'output') }, events.observe('add', callback, undefined, true));
    return server;
  }, { responseMode: 'sse' });
  const app = express(), nodeHandler = toNodeHandler(handler);
  app.use('/mcp', events.middleware, express.json(), events.describe);
  app.all('/mcp', (req, res, next) => { void nodeHandler(req, res, req.body).catch(next); });
  const listener = app.listen(0, '127.0.0.1');
  await once(listener, 'listening');
  t.after(async () => { await handler.close(); await new Promise(resolve => listener.close(resolve)); });
  const response = await call(`http://127.0.0.1:${listener.address().port}/mcp`, '2026-07-28', 'add', {});
  assert.match(await response.text(), /"isError":true/);
  const { request, callbacks } = requestEvent(logs, response);
  assert.equal(request.outcome, expected);
  assert.equal(callbacks.length, 1);
  assert.ok(!JSON.stringify(logs).includes('private-'));
});
