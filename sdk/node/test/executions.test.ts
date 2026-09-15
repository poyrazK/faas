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
