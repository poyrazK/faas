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

test('worker publisher pushes bounded task backlog and oldest age without identifiers', async () => {
  const requests = [];
  let finish;
  const completed = new Promise(resolve => { finish = resolve; });
  const publisher = startMcpTaskMetricsPublisher({
    store: { async queueMetrics() { return { outstandingTasks: 7, oldestAgeSeconds: 123.5, runningTasks: 2, capacityWaitingTasks: 3, failedTasks: 4, retryWaitingTasks: 5, activeWorkers: 2, drainingWorkers: 0, unsupportedHandlerTasks: 1 }; } },
    appSlug: 'mcp-worker',
    token: 'metrics-write-secret',
    apiURL: 'https://api.example.test',
    intervalMs: 300_000,
    async fetchImpl(url, options) {
      requests.push({ url, options });
      if (requests.length === 9) finish();
      return { status: 204 };
    },
  });

  await Promise.race([completed, new Promise((_, reject) => setTimeout(() => reject(new Error('metric pushes timed out')), 1000))]);
  await publisher.close();
  assert.deepEqual(requests.map(request => request.url).sort(), [
    'https://api.example.test/v1/apps/mcp-worker/custom-metrics/mcp_tasks_active_workers',
    'https://api.example.test/v1/apps/mcp-worker/custom-metrics/mcp_tasks_capacity_waiting',
    'https://api.example.test/v1/apps/mcp-worker/custom-metrics/mcp_tasks_draining_workers',
    'https://api.example.test/v1/apps/mcp-worker/custom-metrics/mcp_tasks_failed',
    'https://api.example.test/v1/apps/mcp-worker/custom-metrics/mcp_tasks_oldest_age_seconds',
    'https://api.example.test/v1/apps/mcp-worker/custom-metrics/mcp_tasks_outstanding',
    'https://api.example.test/v1/apps/mcp-worker/custom-metrics/mcp_tasks_retry_waiting',
    'https://api.example.test/v1/apps/mcp-worker/custom-metrics/mcp_tasks_running',
    'https://api.example.test/v1/apps/mcp-worker/custom-metrics/mcp_tasks_unsupported_handler_tasks',
  ]);
  for (const request of requests) {
    assert.equal(request.options.method, 'PUT');
    assert.equal(request.options.redirect, 'error');
    assert.equal(request.options.headers.authorization, 'Bearer metrics-write-secret');
    assert.deepEqual(JSON.parse(request.options.body), {
      value: ({ mcp_tasks_outstanding: 7, mcp_tasks_oldest_age_seconds: 123.5, mcp_tasks_running: 2, mcp_tasks_capacity_waiting: 3, mcp_tasks_failed: 4, mcp_tasks_retry_waiting: 5, mcp_tasks_active_workers: 2, mcp_tasks_draining_workers: 0, mcp_tasks_unsupported_handler_tasks: 1 })[request.url.split('/').at(-1)],
    });
    assert.ok(!request.options.body.includes('task_id'));
  }
});

test('publisher reports API failures without stopping its caller', async () => {
  let errors = 0;
  let completed = 0;
  const publisher = startMcpTaskMetricsPublisher({
    store: { async queueMetrics() { return { outstandingTasks: 1, oldestAgeSeconds: 1, runningTasks: 1, capacityWaitingTasks: 0, failedTasks: 0, retryWaitingTasks: 0, activeWorkers: 1, drainingWorkers: 0, unsupportedHandlerTasks: 0 }; } },
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


test('only a successful observer reporting cycle publishes the observer heartbeat', async () => {
  const metrics = { outstandingTasks: 0, oldestAgeSeconds: 0, runningTasks: 0, capacityWaitingTasks: 0, failedTasks: 0, retryWaitingTasks: 0, activeWorkers: 0, drainingWorkers: 0, unsupportedHandlerTasks: 0 };
  for (const success of [true, false]) {
    const urls = [];
    const publisher = startMcpTaskMetricsPublisher({ store: { async queueMetrics() { return metrics; } }, role: 'observer', appSlug: 'worker', token: 'secret', async fetchImpl(url) { urls.push(url); return { status: success ? 204 : 500 }; } });
    await publisher.close();
    assert.equal(urls.some(url => url.endsWith('mcp_tasks_observer_heartbeat')), success);
    if (success) assert.ok(urls.at(-1).endsWith('mcp_tasks_observer_heartbeat'));
  }
});
