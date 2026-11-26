import assert from 'node:assert/strict';
import test from 'node:test';
import {
  createMcpTaskSubmissionFetch, lookupMcpTaskBySubmissionKey, submitMcpTask, type McpTaskSubmissionConnection,
} from '../src/preview.js';

const key = '019b0000-0000-7123-8123-0123456789ab';
const submit = 'gregale/tasks/submit';
const lookup = 'gregale/tasks/lookup';
const protocol = '2026-07-28';
const task = { taskId: 'saved task', status: 'working', createdAt: '2026-11-20T01:00:00Z',
  lastUpdatedAt: '2026-11-20T01:00:00Z', ttlMs: 360_000 };
const submission = { submissionKey: key, toolName: 'build_report', arguments: { report: 'weekly' } };

function connection(request: McpTaskSubmissionConnection['request']): McpTaskSubmissionConnection {
  return {
    getNegotiatedProtocolVersion: () => protocol,
    getServerCapabilities: () => ({ extensions: { 'io.modelcontextprotocol/tasks': {},
      'io.gregale/task-submission': {}, 'io.gregale/task-submission-lookup': {} } }),
    request,
  };
}

function init(method: string, params: object, id = 7): RequestInit {
  return {
    method: 'POST',
    headers: { 'Mcp-Method': method, 'Mcp-Protocol-Version': protocol, Authorization: 'Bearer fixture' },
    body: JSON.stringify({ jsonrpc: '2.0', id, method, params }),
  };
}

test('the keyed transport sends one standard tool body with the keyed method marker and Task metadata', async () => {
  const reply = { jsonrpc: '2.0', id: 7, result: { resultType: 'task', ...task, statusMessage: 'private message' } };
  const response = () => new Response(JSON.stringify(reply), { headers: { 'Content-Type': 'application/json; charset=utf-8' } });
  const wire = createMcpTaskSubmissionFetch(async (_, request) => {
    const headers = new Headers(request?.headers);
    assert.equal(headers.get('Mcp-Method'), submit);
    assert.equal(headers.get('Mcp-Name'), '=?base64?csOpcG9ydA==?=');
    assert.equal(headers.get('Authorization'), 'Bearer fixture');
    assert.deepEqual(JSON.parse(String(request?.body)), {
      jsonrpc: '2.0', id: 7, method: 'tools/call', params: { name: 'réport', arguments: { report: 'weekly' }, _meta: {
        'io.gregale/task-submission-key': key,
        'io.modelcontextprotocol/clientCapabilities': { extensions: { 'io.modelcontextprotocol/tasks': {} }, elicitation: { form: {} } },
        'io.modelcontextprotocol/protocolVersion': protocol,
      } },
    });
    return response();
  });
  const client = connection(async ({ method, params }, schema, { allowInputRequired, timeout }) => {
    assert.equal(method, submit);
    assert.equal(schema['~standard'].version, 1);
    assert.equal(timeout, 5_000);
    assert.equal(allowInputRequired, true);
    assert.ok('_meta' in params);
    // Model the official client envelope and result projection.
    const sent = { ...params, _meta: { ...params._meta, 'io.modelcontextprotocol/protocolVersion': protocol } };
    const { result } = await (await wire('https://mcp.example/mcp', init(method, sent))).json();
    assert.equal(result.resultType, 'complete');
    return result;
  });
  const received = await submitMcpTask(client, { ...submission, toolName: 'réport', deferFormInput: true }, { timeoutMs: 5_000 });
  assert.deepEqual(received, { outcome: 'accepted', task: { ...task, nextAction: 'poll_status' } });
});

test('the transport leaves ordinary calls intact and blocks malformed keyed envelopes before fetch', async () => {
  const original = init('tools/call', { name: 'build_report' });
  let calls = 0;
  const wire = createMcpTaskSubmissionFetch(async (_, request) => {
    calls++;
    assert.equal(request, original);
    return new Response('ordinary response');
  });
  assert.equal(await (await wire('https://mcp.example/mcp', original)).text(), 'ordinary response');
  await assert.rejects(wire('https://mcp.example/mcp', init(submit, { name: 'build_report' })), TypeError);
  assert.equal(calls, 1);
});

test('submit keeps a lost or unrecognized acknowledgement unconfirmed and never retries', async () => {
  for (const outcome of [undefined, { handle: { task } }, { resultType: 'task', ...task }]) {
    let calls = 0;
    const client = connection(async ({ method }) => {
      assert.equal(method, submit);
      calls++;
      if (outcome === undefined) throw new Error('private transport failure');
      return outcome;
    });
    assert.deepEqual(await submitMcpTask(client, submission), { outcome: 'unconfirmed' });
    assert.equal(calls, 1);
  }
});

test('definite tool rejection is sanitized and admission is checked before sending', async () => {
  const denied = connection(async () => ({ isError: true, content: [{ text: 'private error detail' }] }));
  await assert.rejects(submitMcpTask(denied, submission), {
    name: 'McpTaskSubmissionError', code: 'rejected', message: 'MCP server rejected the keyed Task submission',
  });
  const unused = connection(async () => assert.fail('invalid mutation was sent'));
  for (const submissionKey of [key + '\n', key.toUpperCase(), '019b0000-0000-4123-8123-0123456789ab']) {
    await assert.rejects(submitMcpTask(unused, { ...submission, submissionKey }), TypeError);
  }
  await assert.rejects(submitMcpTask(unused, { ...submission, arguments: { count: Number.NaN } }), TypeError);
  await assert.rejects(submitMcpTask({ ...unused, getServerCapabilities: () => ({}) }, submission), { code: 'unavailable' });
});

test('lookup reads metadata using an old key and rejects payload-bearing responses', async () => {
  const completed = { ...task, status: 'completed' };
  const client = connection(async (request, _, options) => {
    assert.deepEqual(request, { method: lookup, params: { submissionKey: key } });
    assert.equal(options.allowInputRequired, true);
    return completed;
  });
  assert.deepEqual(await lookupMcpTaskBySubmissionKey(client, key), { ...completed, nextAction: 'read_result' });
  const malformed = connection(async () => ({ ...completed, result: { content: [] } }));
  await assert.rejects(lookupMcpTaskBySubmissionKey(malformed, key), { code: 'invalid_response' });
  const foreign = connection(async () => { throw Object.assign(new Error('private response'), { code: -32002 }); });
  await assert.rejects(lookupMcpTaskBySubmissionKey(foreign, key), {
    code: 'not_found', message: 'MCP Task is unavailable to this caller',
  });
});

test('a keyed SSE response uses the matching terminal frame and releases the stream', async () => {
  const frame = JSON.stringify({ jsonrpc: '2.0', id: 7, result: { resultType: 'task', ...task } });
  const other = JSON.stringify({ jsonrpc: '2.0', id: 8, result: { resultType: 'task', ...task, taskId: 'other' } });
  const encoder = new TextEncoder();
  let cancelled = false;
  const body = new ReadableStream<Uint8Array>({
    start(controller) {
      controller.enqueue(encoder.encode(`event: ping\r\ndata: ignored\r\n\r\ndata: ${other}\r\n\r\n`));
      controller.enqueue(encoder.encode(`event: message\r\ndata: ${frame.slice(0, 12)}`));
      controller.enqueue(encoder.encode(`${frame.slice(12)}\r\n\r\n`));
    },
    cancel() { cancelled = true; },
  });
  const wire = createMcpTaskSubmissionFetch(async () => new Response(body, {
    headers: { 'Content-Type': 'text/event-stream; charset=utf-8' },
  }));
  const client = connection(async ({ method, params }) => {
    assert.ok('_meta' in params);
    const sent = { ...params, _meta: { ...params._meta, 'io.modelcontextprotocol/protocolVersion': protocol } };
    const response = await wire('https://mcp.example/mcp', init(method, sent));
    assert.equal(response.headers.get('Content-Type'), 'application/json');
    return (await response.json()).result;
  });
  assert.deepEqual(await submitMcpTask(client, submission), {
    outcome: 'accepted', task: { ...task, nextAction: 'poll_status' },
  });
  await new Promise<void>(resolve => setImmediate(resolve));
  assert.equal(cancelled, true);
});
