import test from 'node:test';
import assert from 'node:assert/strict';

import {
  AI_SESSION_HEADER,
  AI_USER_HEADER,
  AI_TAGS_HEADER,
  AI_TRACE_HEADER,
  AIGatewayError,
  aiGatewayFeedback,
  aiGatewayHeaders,
  aiGatewayURL,
  isValidAIUserId,
  opaqueAIUserId,
  withAIGatewayHeaders,
} from '../src/preview.js';

test('aiGatewayURL addresses an integration or a route on the relay', () => {
  const env = { GREGALE_OUTBOUND_URL: 'http://127.0.0.1:2774/' };
  assert.equal(aiGatewayURL({ integration: 'int-1' }, { env }), 'http://127.0.0.1:2774/i/int-1');
  assert.equal(aiGatewayURL({ route: 'route-1' }, { env }), 'http://127.0.0.1:2774/r/route-1');
  assert.throws(() => aiGatewayURL({ route: 'route-1' }, { env: {} }), AIGatewayError);
  assert.throws(() => aiGatewayURL({ route: 'a/b' }, { env }), AIGatewayError);
});

test('aiGatewayHeaders validates IDs like the gateway', () => {
  assert.deepEqual(aiGatewayHeaders({ user: 'user:42', session: 'chat-7' }), {
    [AI_USER_HEADER]: 'user:42',
    [AI_SESSION_HEADER]: 'chat-7',
  });
  assert.deepEqual(aiGatewayHeaders({}), {});
  for (const bad of ['', 'has space', 'x'.repeat(65), '~other']) {
    assert.equal(isValidAIUserId(bad), false);
    assert.throws(() => aiGatewayHeaders({ user: bad }), AIGatewayError);
  }
});

test('opaqueAIUserId is stable, valid and keyed', () => {
  const id = opaqueAIUserId('alice@example.com');
  assert.equal(id, opaqueAIUserId('alice@example.com'));
  assert.ok(isValidAIUserId(id));
  assert.notEqual(opaqueAIUserId('alice@example.com', 'secret'), id);
  assert.ok(isValidAIUserId(opaqueAIUserId('alice@example.com', 'secret')));
});

test('withAIGatewayHeaders adds the headers to every call', async () => {
  const seen: Headers[] = [];
  const fetchImpl = (async (_input: RequestInfo | URL, init?: RequestInit) => {
    seen.push(new Headers(init?.headers));
    return new Response('{}');
  }) as typeof fetch;
  const wrapped = withAIGatewayHeaders(fetchImpl, { user: 'user:42' });
  await wrapped('http://127.0.0.1:2774/r/route-1/v1/chat/completions', { headers: { 'Content-Type': 'application/json' } });
  assert.equal(seen[0]?.get(AI_USER_HEADER), 'user:42');
  assert.equal(seen[0]?.get('content-type'), 'application/json');
  assert.equal(seen[0]?.get(AI_SESSION_HEADER), null);
});

test('aiGatewayHeaders names tags and a trace', () => {
  const headers = aiGatewayHeaders({ tags: { feature: 'search', tier: 'free' }, trace: 'task-7' });
  assert.equal(headers[AI_TAGS_HEADER], 'feature=search,tier=free');
  assert.equal(headers[AI_TRACE_HEADER], 'task-7');
  assert.throws(() => aiGatewayHeaders({ tags: { Feature: 'search' } }), AIGatewayError);
  assert.throws(() => aiGatewayHeaders({ tags: { a: '1', b: '2', c: '3', d: '4', e: '5', f: '6' } }), AIGatewayError);
  assert.throws(() => aiGatewayHeaders({ trace: 'has space' }), AIGatewayError);
});

test('aiGatewayFeedback rates a call through the relay', async () => {
  const seen: { url: string; body: string }[] = [];
  const fetchImpl = (async (input: RequestInfo | URL, init?: RequestInit) => {
    seen.push({ url: String(input), body: String(init?.body) });
    return new Response(null, { status: 204 });
  }) as typeof fetch;
  const env = { GREGALE_OUTBOUND_URL: 'http://127.0.0.1:2774' };
  await aiGatewayFeedback({ integration: 'int-1', callId: 'call-1', rating: 'bad', label: 'wrong_answer', env, fetch: fetchImpl });
  assert.equal(seen[0]?.url, 'http://127.0.0.1:2774/i/int-1/_gregale/feedback');
  assert.deepEqual(JSON.parse(seen[0]?.body ?? '{}'), { call_id: 'call-1', rating: 'bad', label: 'wrong_answer' });
  await assert.rejects(aiGatewayFeedback({ integration: 'int-1', callId: 'call-1', rating: 'bad', label: 'Wrong', env, fetch: fetchImpl }), AIGatewayError);
  const refused = (async () => new Response(null, { status: 404 })) as typeof fetch;
  await assert.rejects(aiGatewayFeedback({ integration: 'int-1', callId: 'call-1', rating: 'good', env, fetch: refused }), AIGatewayError);
});
