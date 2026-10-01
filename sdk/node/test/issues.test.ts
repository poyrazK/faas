import test from 'node:test';
import assert from 'node:assert/strict';
import { createIssueReporter } from '../src/issues.js';

test('exception reporting retries the same event and keeps request context', async () => {
  const requests: Array<Record<string, unknown>> = [];
  let attempt = 0;
  const reporter = createIssueReporter({ baseURL: 'https://api.gregale.dev', app: 'exports', token: 'g_issue_test',
    fetch: (async (_url, options) => {
      requests.push(JSON.parse(String(options?.body)));
      assert.equal(new Headers(options?.headers).get('Authorization'), 'Bearer g_issue_test');
      if (attempt++ === 0) throw new Error('connection failed');
      return new Response('{}', { status: 202 });
    }) as typeof fetch });
  const id = reporter.captureException(new TypeError('invalid format'), { request_id: 'request', route: '/exports' });
  assert.equal(await reporter.flush(), false);
  assert.equal(await reporter.flush(), true);
  assert.equal(requests.length, 2);
  assert.equal(requests[0]?.event_id, id);
  assert.deepEqual(requests[0], requests[1]);
  assert.equal(requests[0]?.request_id, 'request');
  assert.ok(Array.isArray(requests[0]?.frames));
  await reporter.close();
});

test('queue overflow is visible and wrapping preserves the original failure', async () => {
  const reporter = createIssueReporter({ baseURL: 'https://api.gregale.dev', app: 'exports', token: 'g_issue_test', maxQueue: 1,
    fetch: (async () => new Response('{}', { status: 503 })) as typeof fetch });
  const original = new Error('original');
  await assert.rejects(reporter.wrap(async () => { throw original; }), error => error === original);
  assert.equal(reporter.captureException(new Error('overflow')), undefined);
  assert.equal(reporter.stats().dropped, 1);
  assert.equal(await reporter.close(), false);
});
