import test from 'node:test';
import assert from 'node:assert/strict';
import { resolveMcpTaskMetricsSettings, startMcpTaskMetricsPublisher } from '../task-metrics.js';

test('task scaling is optional and requires both credentials on a dedicated worker', () => {
  assert.equal(resolveMcpTaskMetricsSettings({ env: {}, role: 'worker' }), undefined);
  assert.throws(() => resolveMcpTaskMetricsSettings({ env: { MCP_TASKS_SCALING_TOKEN: 'secret' }, role: 'worker' }), /requires both/);
  assert.throws(() => resolveMcpTaskMetricsSettings({
    env: { MCP_TASKS_SCALING_APP_SLUG: 'mcp-worker', MCP_TASKS_SCALING_TOKEN: 'secret' }, role: 'web',
  }), /dedicated worker role/);
  assert.throws(() => resolveMcpTaskMetricsSettings({
    env: { MCP_TASKS_SCALING_APP_SLUG: 'mcp-worker', MCP_TASKS_SCALING_TOKEN: 'secret', GREGALE_API_URL: 'http://api.example.test' }, role: 'worker',
  }), /must be HTTPS/);
  assert.deepEqual(resolveMcpTaskMetricsSettings({
    env: { MCP_TASKS_SCALING_APP_SLUG: 'mcp-worker', MCP_TASKS_SCALING_TOKEN: 'secret' }, role: 'worker',
  }), { appSlug: 'mcp-worker', token: 'secret', apiURL: 'https://api.gregale.dev' });
});

test('worker publisher pushes bounded task backlog, age, and handler compatibility without identifiers', async () => {
  const requests = [];
  let finish;
  const completed = new Promise(resolve => { finish = resolve; });
  const publisher = startMcpTaskMetricsPublisher({
    store: { async queueMetrics() { return { outstandingTasks: 7, oldestAgeSeconds: 123.5, withoutCompatibleWorkerTasks: 2 }; } },
    appSlug: 'mcp-worker',
    token: 'metrics-write-secret',
    apiURL: 'https://api.example.test',
    intervalMs: 300_000,
    async fetchImpl(url, options) {
      requests.push({ url, options });
      if (requests.length === 3) finish();
      return { status: 204 };
    },
  });

  await Promise.race([completed, new Promise((_, reject) => setTimeout(() => reject(new Error('metric pushes timed out')), 1000))]);
  await publisher.close();
  assert.deepEqual(requests.map(request => request.url).sort(), [
    'https://api.example.test/v1/apps/mcp-worker/custom-metrics/mcp_tasks_oldest_age_seconds',
    'https://api.example.test/v1/apps/mcp-worker/custom-metrics/mcp_tasks_outstanding',
    'https://api.example.test/v1/apps/mcp-worker/custom-metrics/mcp_tasks_without_compatible_worker',
  ]);
  for (const request of requests) {
    assert.equal(request.options.method, 'PUT');
    assert.equal(request.options.redirect, 'error');
    assert.equal(request.options.headers.authorization, 'Bearer metrics-write-secret');
    assert.deepEqual(JSON.parse(request.options.body), {
      value: request.url.endsWith('mcp_tasks_outstanding') ? 7
        : request.url.endsWith('mcp_tasks_oldest_age_seconds') ? 123.5 : 2,
    });
    assert.ok(!request.options.body.includes('task_id'));
  }
});

test('only the observer role publishes the dedicated observer heartbeat', async () => {
  const requests = [];
  let finish;
  const completed = new Promise(resolve => { finish = resolve; });
  const publisher = startMcpTaskMetricsPublisher({
    store: { async queueMetrics() { return { outstandingTasks: 0, oldestAgeSeconds: 0, withoutCompatibleWorkerTasks: 0 }; } },
    appSlug: 'mcp-worker',
    token: 'metrics-write-secret',
    apiURL: 'https://api.example.test',
    intervalMs: 300_000,
    observer: true,
    async fetchImpl(url, options) {
      requests.push({ url, options });
      if (requests.length === 4) finish();
      return { status: 204 };
    },
  });

  await Promise.race([completed, new Promise((_, reject) => setTimeout(() => reject(new Error('observer metrics timed out')), 1000))]);
  await publisher.close();
  const heartbeat = requests.find(request => request.url.endsWith('/mcp_tasks_observer_heartbeat'));
  assert.ok(heartbeat);
  assert.deepEqual(JSON.parse(heartbeat.options.body), { value: 1 });
  assert.equal(requests.filter(request => request.url.endsWith('/mcp_tasks_observer_heartbeat')).length, 1);
});

test('observer health failures withhold queue gauges and the heartbeat', async () => {
  const requests = [];
  let errors = 0;
  const publisher = startMcpTaskMetricsPublisher({
    store: { async queueMetrics() { throw new Error('postgres://private-user:secret@db.example/production'); } },
    appSlug: 'mcp-worker',
    token: 'metrics-write-secret',
    apiURL: 'https://api.example.test',
    intervalMs: 300_000,
    observer: true,
    async fetchImpl(url) {
      requests.push(url);
      return { status: 204 };
    },
    onError() { errors++; },
  });

  await publisher.close();
  assert.deepEqual(requests, []);
  assert.equal(errors, 1);
});

test('publisher reports API failures without stopping its caller', async () => {
  let errors = 0;
  let completed = 0;
  const publisher = startMcpTaskMetricsPublisher({
    store: { async queueMetrics() { return { outstandingTasks: 1, oldestAgeSeconds: 1, withoutCompatibleWorkerTasks: 0 }; } },
    appSlug: 'mcp-worker', token: 'secret', intervalMs: 300_000,
    async fetchImpl() { return { status: 503 }; },
    onError() { errors++; },
  });
  await new Promise(resolve => setTimeout(resolve, 20));
  await publisher.close();
  completed++;
  assert.equal(completed, 1);
  assert.equal(errors, 1);
});
