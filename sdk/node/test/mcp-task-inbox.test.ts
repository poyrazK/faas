import assert from 'node:assert/strict';
import test from 'node:test';
import { listTaskInbox, McpTaskInboxError, watchTaskInbox, type McpTaskInboxConnection } from '../src/preview.js';

const timestamp = '2026-11-24T01:00:00.123456789Z';
const changedAt = '2026-11-24T01:00:00.123456790Z';
const cursor = 'anchor.' + 'a'.repeat(43);

function entry(taskId: string, lastUpdatedAt = timestamp) {
  return { resultType: 'complete', taskId, status: 'input_required', createdAt: timestamp,
    lastUpdatedAt, ttlMs: 60_000, pollIntervalMs: 500 };
}

function page(tasks: unknown[], nextCursor?: string) {
  return { tasks, ...(nextCursor ? { nextCursor } : {}) };
}

function connection(request: McpTaskInboxConnection['request']): McpTaskInboxConnection {
  return {
    getNegotiatedProtocolVersion: () => '2026-07-28',
    getServerCapabilities: () => ({ extensions: { 'io.modelcontextprotocol/tasks': {}, 'io.gregale/task-inbox': {} } }),
    request,
  };
}

test('listTaskInbox forwards a scoped query and projects metadata from the complete page', async () => {
  const next = 'next.' + 'a'.repeat(43);
  const client = connection(async (request, schema, options) => {
    assert.deepEqual(request, { method: 'gregale/tasks/inbox', params: { limit: 1, status: 'input_required', cursor } });
    assert.equal(schema['~standard'].version, 1);
    assert.equal(options.allowInputRequired, true);
    assert.equal(options.timeout, 3_000);
    return page([{ ...entry('opaque task id'), ttlMs: null, privateMetadata: 'discarded' }], next);
  });
  assert.deepEqual(await listTaskInbox(client, { status: 'input_required', limit: 1, cursor, timeoutMs: 3_000 }), {
    tasks: [{ taskId: 'opaque task id', status: 'input_required', nextAction: 'provide_input',
      createdAt: timestamp, lastUpdatedAt: timestamp, ttlMs: null, pollIntervalMs: 500 }],
    nextCursor: next,
  });
});

test('listTaskInbox checks the revision, extension and query before requesting', async () => {
  const client = connection(async () => assert.fail('unqualified requests must stop locally'));
  await assert.rejects(listTaskInbox({ ...client, getNegotiatedProtocolVersion: () => '2025-11-25' }),
    { name: 'McpTaskInboxError', code: 'unavailable' });
  await assert.rejects(listTaskInbox({ ...client, getServerCapabilities: () => ({ extensions: {} }) }),
    { name: 'McpTaskInboxError', code: 'unavailable' });
  await assert.rejects(listTaskInbox(client, { limit: 51 }), TypeError);
  await assert.rejects(listTaskInbox(client, { cursor: cursor + '\n' }), TypeError);
});

test('listTaskInbox rejects malformed, partial and payload-containing pages', async () => {
  const invalidPages = [
    { resultType: 'input_required', inputRequests: {} },
    page([entry('duplicate'), entry('duplicate')]),
    page([{ ...entry('bad-stamp'), lastUpdatedAt: 'yesterday' }]),
    page([{ ...entry('payload'), InputRequests: { private: 'redacted fixture' } }]),
    page([{ ...entry('wrong-scope'), status: 'working' }]),
    page([entry('short')], cursor),
    page([entry('one'), entry('two')], cursor),
  ];
  for (const value of invalidPages) {
    const client = connection(async () => value);
    await assert.rejects(listTaskInbox(client, { status: 'input_required', limit: 2, cursor }),
      { name: 'McpTaskInboxError', code: 'invalid_page', message: 'Invalid MCP Task inbox page' });
  }
  const denied = connection(async () => { throw Object.assign(new Error('private upstream body'), { status: 403 }); });
  await assert.rejects(listTaskInbox(denied), {
    name: 'McpTaskInboxError', code: 'request_failed', message: 'MCP Task inbox request failed', status: 403,
  });
});

test('watchTaskInbox restarts at page one after a failed page and emits only changed observations', { timeout: 10_000 }, async () => {
  const cursors: (string | undefined)[] = [];
  const client = connection(async ({ params }) => {
    cursors.push(params.cursor);
    switch (cursors.length) {
      case 1: return page([entry('partial')], cursor);
      case 2: throw Object.assign(new Error('temporary fixture response'), { status: 503 });
      case 3:
      case 4: return page([entry('retained')]);
      case 5: return page([entry('retained', changedAt)]);
      default: assert.fail('unexpected inbox read');
    }
  });
  const watch = watchTaskInbox(client, { limit: 1, intervalMs: 1_000 });
  try {
    const first = await watch.next();
    assert.equal(first.value?.taskId, 'retained');
    assert.equal(first.value?.lastUpdatedAt, timestamp);
    const changed = await watch.next();
    assert.equal(changed.value?.lastUpdatedAt, changedAt);
    assert.deepEqual(cursors, [undefined, cursor, undefined, undefined, undefined]);
  } finally {
    await watch.return();
  }
});

test('watchTaskInbox rejects cursor cycles and scans over the page cap before yielding', async () => {
  for (const cycle of [true, false]) {
    let reads = 0;
    const client = connection(async () => {
      reads++;
      const anchor = cycle ? reads % 2 : reads;
      return page([entry(String(reads))], `anchor${anchor}.` + 'a'.repeat(43));
    });
    const watch = watchTaskInbox(client, { limit: 1 });
    await assert.rejects(watch.next(), (error: unknown) =>
      error instanceof McpTaskInboxError && error.code === (cycle ? 'invalid_page' : 'scan_limit'));
    assert.equal(reads, cycle ? 3 : 64);
  }
});

test('aborting or closing a watch cancels its pending read', { timeout: 4_000 }, async () => {
  for (const stop of ['abort', 'return'] as const) {
    let receivedSignal: AbortSignal | undefined;
    const client = connection(async (_request, _schema, { signal }) => {
      receivedSignal = signal;
      return new Promise<unknown>((_resolve, reject) => {
        signal.addEventListener('abort', () => reject(signal.reason), { once: true });
      });
    });
    const controller = new AbortController();
    const watch = watchTaskInbox(client, { signal: controller.signal });
    const wait = assert.rejects(watch.next(), { name: 'AbortError' });
    assert.ok(receivedSignal);
    if (stop === 'abort') controller.abort();
    else await watch.return();
    await wait;
    assert.equal(receivedSignal.aborted, true);
    assert.equal((await watch.next()).done, true);
  }
});
