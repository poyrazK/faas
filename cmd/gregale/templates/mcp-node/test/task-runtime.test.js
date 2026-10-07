import test from 'node:test';
import assert from 'node:assert/strict';
import { resolveMcpTaskSettings, startMcpTaskRuntime } from '../task-runtime.js';

const config = {
  tasks: {
    enabled: true,
    database_url_env: 'DATABASE_URL',
    owner_key_env: 'MCP_TASK_OWNER_KEY',
    namespace_env: 'FAAS_APP_ID',
    ttl_seconds: 60,
    poll_interval_ms: 500,
    worker_concurrency: 2,
  },
};

const baseEnv = {
  DATABASE_URL: 'postgres://example.invalid/tasks',
  MCP_TASK_OWNER_KEY: 'x'.repeat(48),
  FAAS_APP_ID: 'web-app-id',
};

test('a dedicated worker requires and uses the shared logical namespace override', () => {
  assert.throws(() => resolveMcpTaskSettings(config, { env: baseEnv, role: 'worker' }), /MCP_TASK_NAMESPACE/);
  const settings = resolveMcpTaskSettings(config, {
    env: { ...baseEnv, MCP_TASK_NAMESPACE: 'storefront-prod' },
    role: 'worker',
  });
  assert.equal(settings.namespace, 'storefront-prod');
  assert.equal(settings.role, 'worker');
});

test('the explicit namespace overrides the per-app ID for the web process too', () => {
  assert.throws(() => resolveMcpTaskSettings(config, { env: baseEnv, role: 'web' }), /MCP_TASK_NAMESPACE/);
  const settings = resolveMcpTaskSettings(config, {
    env: { ...baseEnv, MCP_TASK_NAMESPACE: 'storefront-prod' },
    role: 'web',
  });
  assert.equal(settings.namespace, 'storefront-prod');
});

test('the dedicated worker refuses a configuration with Tasks disabled', () => {
  assert.throws(() => resolveMcpTaskSettings({ tasks: { enabled: false } }, { role: 'worker' }), /tasks.enabled=true/);
});

test('runtime startup selects the process role and closes the runtime and pool once', async () => {
  let runtimeOptions;
  let poolCloseCount = 0;
  const fakeRuntime = {
    async start() {},
    async stop() {},
  };
  const service = await startMcpTaskRuntime(config, {
    env: { ...baseEnv, MCP_TASK_NAMESPACE: 'storefront-prod' },
    role: 'worker',
    createPool(options) {
      assert.equal(options.connectionString, baseEnv.DATABASE_URL);
      return { async end() { poolCloseCount++; } };
    },
    createStore(options) {
      assert.equal(options.namespace, 'storefront-prod');
      assert.equal(options.ownerKey, baseEnv.MCP_TASK_OWNER_KEY);
      return {};
    },
    createRuntime(options) {
      runtimeOptions = options;
      return fakeRuntime;
    },
  });

  assert.equal(service.taskRuntime, fakeRuntime);
  assert.equal(runtimeOptions.workerEnabled, true);
  assert.equal(runtimeOptions.keepAlive, true);
  await service.close();
  await service.close();
  assert.equal(poolCloseCount, 1);
});

test('dedicated workers enable the optional metrics publisher with the scoped app credentials', async () => {
  let publishedSettings;
  let publisherCloseCount = 0;
  const service = await startMcpTaskRuntime(config, {
    env: {
      ...baseEnv,
      MCP_TASK_NAMESPACE: 'storefront-prod',
      MCP_TASKS_SCALING_APP_SLUG: 'storefront-worker',
      MCP_TASKS_SCALING_TOKEN: 'metrics-write-secret',
      GREGALE_API_URL: 'https://api.staging.gregale.dev',
    },
    role: 'worker',
    createPool: () => ({ async end() {} }),
    createStore: () => ({ queueMetrics: async () => ({ outstandingTasks: 3, oldestAgeSeconds: 4 }) }),
    createRuntime: () => ({ async start() {}, async stop() {} }),
    createMetricsPublisher(options) {
      publishedSettings = options;
      return { async close() { publisherCloseCount++; } };
    },
  });

  assert.equal(publishedSettings.appSlug, 'storefront-worker');
  assert.equal(publishedSettings.token, 'metrics-write-secret');
  assert.equal(publishedSettings.apiURL, 'https://api.staging.gregale.dev');
  assert.equal(typeof publishedSettings.store.queueMetrics, 'function');
  await service.close();
  assert.equal(publisherCloseCount, 1);
});

test('scaling metric credentials are rejected on the web role', async () => {
  await assert.rejects(startMcpTaskRuntime(config, {
    env: {
      ...baseEnv,
      MCP_TASK_NAMESPACE: 'storefront-prod',
      MCP_TASKS_SCALING_APP_SLUG: 'storefront-worker',
      MCP_TASKS_SCALING_TOKEN: 'metrics-write-secret',
    },
    role: 'web',
    createPool() { throw new Error('pool must not start for invalid role settings'); },
  }), /only be published by the dedicated worker role/);
});

test('web process startup exposes task APIs without claiming work', async () => {
  let runtimeOptions;
  const service = await startMcpTaskRuntime(config, {
    env: { ...baseEnv, MCP_TASK_NAMESPACE: 'storefront-prod' },
    role: 'web',
    createPool: () => ({ async end() {} }),
    createStore: () => ({}),
    createRuntime: options => {
      runtimeOptions = options;
      return { async start() {}, async stop() {} };
    },
  });

  assert.equal(runtimeOptions.workerEnabled, false);
  assert.equal(runtimeOptions.keepAlive, false);
  await service.close();
});
