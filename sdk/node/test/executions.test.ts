import test from 'node:test';
import assert from 'node:assert/strict';

import { FaaSClient, type ExecutionEvent } from '../src/preview.js';

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
          runtime: 'python313',
          profile: 'python-data-v1',
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
          runtime_image_digest: 'sha256:' + 'a'.repeat(64),
          packages: { numpy: '2.5.3' },
          runtime: 'python313',
          profile: 'python-data-v1',
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
      {
        runtime: 'python313', profile: 'python-data-v1',
        source: 'def main(input, context): return input',
      },
      {
        retryInitialMs: 0,
        retryMaxMs: 0,
        idempotencyKey: 'run-key-1',
        onEvent: (event) => { seen.push(event.type); },
      },
    );
    assert.equal(receipt.id, 'run-1');
    assert.equal(receipt.status, 'succeeded');
    assert.equal(receipt.profile, 'python-data-v1');
    assert.equal(receipt.packages?.numpy, '2.5.3');
    assert.equal(receipt.runtime_image_digest, 'sha256:' + 'a'.repeat(64));
    assert.equal(requests[0]?.body && JSON.parse(requests[0].body).profile, 'python-data-v1');
    assert.deepEqual(seen, ['status', 'stdout', 'terminal']);
    assert.equal(requests.length, 3);
    assert.equal(requests[0]?.method, 'POST');
    assert.equal(requests[0]?.body && JSON.parse(requests[0].body).source, 'def main(input, context): return input');
    assert.equal(requests[0]?.url.pathname, '/v1/executions');
    assert.equal(requests[1]?.url.pathname, '/v1/executions/run-1/events');
    assert.equal(requests[2]?.url.pathname, '/v1/executions/run-1');
  } finally {
    client.uninstall();
  }
});

test('runExecution probes admission before uploading and keeps the same key', async () => {
  const calls: string[] = []; const keys: string[] = []; let admissions = 0;
  const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } });
  const client = new FaaSClient('https://api.example.test', { token: 'test-token', fetch: async (input, init) => {
    const url = new URL(input instanceof Request ? input.url : input.toString());
    const method = init?.method ?? 'GET'; calls.push(`${method} ${url.pathname}`);
    if (url.pathname === '/v1/execution-files') return json({ id: 'file-1', status: 'missing', size_bytes: 3, sha256: 'sha256:' + 'a'.repeat(64), upload_required: true, content_url: '/v1/execution-files/file-1/content' });
    if (method === 'POST' && url.pathname === '/v2/executions') {
      keys.push(new Headers(init?.headers).get('Idempotency-Key') ?? ''); admissions++;
      if (admissions === 1) return json({ status: 409, code: 'execution_files_not_ready', title: 'Run files not ready' }, 409);
      return json({ id: 'run-files', status: 'queued' }, 202);
    }
    if (method === 'PUT') return json({ id: 'file-1', status: 'ready', size_bytes: 3, content_url: '/v1/execution-files/file-1/content' });
    if (url.pathname.endsWith('/events')) return stream('id: 1\nevent: terminal\ndata: {"status":"succeeded"}\n\n');
    if (url.pathname === '/v1/executions/run-files') return json({ id: 'run-files', status: 'succeeded', result_validation: { status: 'invalid', message: 'Run result does not match result_schema' } });
    return json({}, 404);
  }});
  try {
    const receipt = await client.runExecution({ runtime: 'node24', source: 'export default ()=>42', result_schema: { type: 'string' } }, { files: { 'data.bin': new Blob(['abc']) }, idempotencyKey: 'stable-files-key' });
    assert.deepEqual(calls.slice(0, 4), ['POST /v1/execution-files', 'POST /v2/executions', 'PUT /v1/execution-files/file-1/content', 'POST /v2/executions']);
    assert.deepEqual(keys, ['stable-files-key', 'stable-files-key']);
    assert.equal(receipt.status, 'succeeded'); assert.equal(receipt.result_validation?.status, 'invalid');
  } finally { client.uninstall(); }
});

test('extended admission errors never fall back to v1 or upload after uncertainty', async () => {
  const calls: string[] = [];
  const client = new FaaSClient('https://api.example.test', { fetch: async (input, init) => {
    const url = new URL(input instanceof Request ? input.url : input.toString()); calls.push(url.pathname);
    return new Response(JSON.stringify({ status: 404, code: 'not_found', title: 'Unavailable' }), { status: 404, headers: { 'content-type': 'application/json' } });
  }});
  try { await assert.rejects(client.runExecution({ runtime: 'node24', source: 'export default ()=>42', result_schema: false })); assert.deepEqual(calls, ['/v2/executions']); }
  finally { client.uninstall(); }
});


test('five-minute class and custom environments use v2 without silent fallback', async () => {
  const paths: string[] = [];
  const client = new FaaSClient('https://api.example.test', { fetch: async (input, init) => {
    const url = new URL(input instanceof Request ? input.url : input.toString()); paths.push(url.pathname);
    if (url.pathname.endsWith('/events')) return stream('id: 1\nevent: terminal\ndata: {"status":"succeeded"}\n\n');
    if (init?.method !== 'POST') return new Response(JSON.stringify({ id: 'run-five-minute', status: 'succeeded', result: 42 }), { status: 200, headers: { 'content-type': 'application/json' } });
    const body = JSON.parse(String(init?.body));
    assert.equal(body.limits.execution_class, 'extended'); assert.equal(body.limits.timeout_ms, 300000);
    assert.equal(body.environment_id, '12345678-1234-4234-8234-123456789abc');
    return new Response(JSON.stringify({ id: 'run-five-minute', status: 'succeeded', result: 42 }), { status: 202, headers: { 'content-type': 'application/json' } });
  }});
  try {
    const receipt = await client.runExecution({ runtime: 'node24', source: 'export default ()=>42', environment_id: '12345678-1234-4234-8234-123456789abc', limits: { execution_class: 'extended', timeout_ms: 300000 } });
    assert.equal(receipt.result, 42); assert.deepEqual(paths, ['/v2/executions', '/v1/executions/run-five-minute/events', '/v1/executions/run-five-minute']);
  } finally { client.uninstall(); }
});
