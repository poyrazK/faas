import test from 'node:test';
import assert from 'node:assert/strict';

import { FaaSClient, type ExecutionEvent } from '../src/index.js';

const stream = (frames: string) => new Response(frames, {
  status: 200,
  headers: { 'content-type': 'text/event-stream' },
});
test('watchExecution decodes typed events and resumes with the latest cursor', async () => {
  const requests: URL[] = [];
  let calls = 0;
  const client = new FaaSClient('https://api.example.test', {
    token: 'test-token',
    fetch: async (input) => {
      const url = new URL(typeof input === 'string' ? input : input.toString());
      requests.push(url);
      calls += 1;
      if (calls === 1) {
        return stream([
          'id: 1\nevent: status\ndata: {"status":"running"}\n\n',
          'id: 2\nevent: stdout\ndata: {"chunk":"hello\\n"}\n\n',
        ].join(''));
      }
      return stream('id: 3\nevent: terminal\ndata: {"status":"succeeded","exit_code":0}\n\n');
    },
  });

  try {
    const events: ExecutionEvent[] = [];
    for await (const event of client.watchExecution('run/1', {
      retryInitialMs: 0,
      retryMaxMs: 0,
    })) {
      events.push(event);
    }

    assert.deepEqual(events.map((event) => event.type), ['status', 'stdout', 'terminal']);
    assert.equal(events[0]?.id, 1);
    assert.equal(events[0]?.data.status, 'running');
    assert.equal(events[1]?.data.chunk, 'hello\n');
    assert.equal(events[2]?.data.exit_code, 0);
    assert.equal(requests[0]?.pathname, '/v1/executions/run%2F1/events');
    assert.equal(requests[0]?.searchParams.get('after'), null);
    assert.equal(requests[1]?.searchParams.get('after'), '2');
    assert.equal(requests.every((url) => url.searchParams.get('limit') === '100'), true);
  } finally {
    client.uninstall();
  }
});

test('watchExecution surfaces malformed event JSON instead of reconnecting forever', async () => {
  const client = new FaaSClient('https://api.example.test', {
    fetch: async () => stream('id: 1\nevent: status\ndata: nope\n\n'),
  });
  try {
    await assert.rejects(
      async () => {
        for await (const _event of client.watchExecution('run-1', {
          retryInitialMs: 0,
          retryMaxMs: 0,
        })) {
          // The malformed frame rejects before this body can run.
        }
      },
      /invalid JSON/,
    );
  } finally {
    client.uninstall();
  }
});

test('runExecution creates, streams, and fetches the terminal receipt', async () => {
  const requests: Array<{ method: string; url: URL; body?: string }> = [];
  const client = new FaaSClient('https://api.example.test', {
    token: 'test-token',
    fetch: async (input, init) => {
      const url = new URL(typeof input === 'string' ? input : input.toString());
      requests.push({
        method: init?.method ?? 'GET',
        url,
        body: typeof init?.body === 'string' ? init.body : undefined,
      });
      if (init?.method === 'POST' && url.pathname === '/v1/executions') {
        return new Response(JSON.stringify({
          id: 'run-1',
          status: 'queued',
          runtime: 'node22',
          limits: { timeout_ms: 1000 },
          output_truncated: false,
          created_at: '2026-01-01T00:00:00Z',
        }), { status: 202, headers: { 'content-type': 'application/json' } });
      }
      if (url.pathname === '/v1/executions/run-1/events') {
        return stream(
          'id: 1\nevent: status\ndata: {"status":"running"}\n\n' +
          'id: 2\nevent: stdout\ndata: {"chunk":"hello"}\n\n' +
          'id: 3\nevent: terminal\ndata: {"status":"succeeded"}\n\n',
        );
      }
      if (url.pathname === '/v1/executions/run-1') {
        return new Response(JSON.stringify({
          id: 'run-1',
          status: 'succeeded',
          runtime: 'node22',
          limits: { timeout_ms: 1000 },
          result: { ok: true },
          stdout: 'hello',
          output_truncated: false,
          created_at: '2026-01-01T00:00:00Z',
        }), { status: 200, headers: { 'content-type': 'application/json' } });
      }
      return new Response('not found', { status: 404 });
    },
  });

  try {
    const seen: string[] = [];
    const receipt = await client.runExecution(
      { runtime: 'node22', source: "console.log('hello')" },
      {
        retryInitialMs: 0,
        retryMaxMs: 0,
        idempotencyKey: 'run-key-1',
        onEvent: (event) => { seen.push(event.type); },
      },
    );
    assert.equal(receipt.id, 'run-1');
    assert.equal(receipt.status, 'succeeded');
    assert.deepEqual(seen, ['status', 'stdout', 'terminal']);
    assert.equal(requests.length, 3);
    assert.equal(requests[0]?.method, 'POST');
    assert.equal(requests[0]?.body && JSON.parse(requests[0].body).source, "console.log('hello')");
    assert.equal(requests[0]?.url.pathname, '/v1/executions');
    assert.equal(requests[1]?.url.pathname, '/v1/executions/run-1/events');
    assert.equal(requests[2]?.url.pathname, '/v1/executions/run-1');
  } finally {
    client.uninstall();
  }
});
