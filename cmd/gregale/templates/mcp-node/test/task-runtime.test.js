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

test('zero-valued Task settings use the same defaults as omitted settings', async () => {
  const fields = ['ttl_seconds', 'poll_interval_ms', 'worker_concurrency', 'shutdown_timeout_ms', 'max_attempts', 'retry_base_delay_ms', 'retry_max_delay_ms'];
  const zeroTasks = { ...config.tasks };
  const omittedTasks = { ...config.tasks };
  for (const field of fields) {
    zeroTasks[field] = 0;
    delete omittedTasks[field];
  }
  const env = { ...baseEnv, MCP_TASK_NAMESPACE: 'storefront-prod' };
  assert.deepEqual(resolveMcpTaskSettings({ tasks: zeroTasks }, { env, role: 'web' }),
    resolveMcpTaskSettings({ tasks: omittedTasks }, { env, role: 'web' }));
  const service = await startMcpTaskRuntime({ tasks: zeroTasks }, {
    env, role: 'web',
    createPool: options => { assert.equal(options.max, 4); return { async end() {} }; },
    createStore: () => ({ async initialize() {}, async claim() {} }),
  });
  await service.close();
});

test('zero-default handling preserves invalid retry settings and explicit drain overrides', () => {
  for (const field of ['max_attempts', 'retry_base_delay_ms', 'retry_max_delay_ms']) {
    for (const value of [false, true, -1, '0']) {
      assert.throws(() => resolveMcpTaskSettings({ tasks: { ...config.tasks, [field]: value } }, { env: baseEnv }));
    }
  }
  const settings = resolveMcpTaskSettings({ tasks: { ...config.tasks, shutdown_timeout_ms: 0 } }, {
    env: { ...baseEnv, MCP_DRAIN_TIMEOUT_SECONDS: '45' },
  });
  assert.equal(settings.shutdownTimeoutMs, 45_000);
});

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
  const service = await startMcpTaskRuntime({ ...config, tasks: { ...config.tasks, max_attempts: 5, worker_concurrency: 16 } }, {
    env: { ...baseEnv, MCP_TASK_NAMESPACE: 'storefront-prod' },
    role: 'worker',
    createPool(options) {
      assert.equal(options.connectionString, baseEnv.DATABASE_URL);
      assert.equal(options.max, 20);
      return { async end() { poolCloseCount++; } };
    },
    createStore(options) {
      assert.equal(options.namespace, 'storefront-prod');
      assert.equal(options.ownerKey, baseEnv.MCP_TASK_OWNER_KEY);
      assert.equal(options.maxAttempts, 5);
      return {};
    },
    createRuntime(options) {
      runtimeOptions = options;
      return fakeRuntime;
    },
  });

  assert.equal(service.taskRuntime, fakeRuntime);
  assert.equal(runtimeOptions.workerEnabled, true);
  assert.equal(runtimeOptions.workerConcurrency, 16);
  assert.equal(runtimeOptions.keepAlive, true);
  assert.equal(runtimeOptions.maxAttempts, 5);
  await service.close();
  await service.close();
  assert.equal(poolCloseCount, 1);
});

// A database restart drops idle pool clients and node-postgres emits 'error'
// on the pool; without a listener that crashes the whole runtime process.
test('runtime keeps an idle pool error from crashing the process', async () => {
  const { EventEmitter } = await import('node:events');
  const pool = Object.assign(new EventEmitter(), { async end() {} });
  const service = await startMcpTaskRuntime(config, {
    env: { ...baseEnv, MCP_TASK_NAMESPACE: 'storefront-prod' },
    role: 'worker',
    createPool: () => pool,
    createStore: () => ({}),
    createRuntime: () => ({ async start() {}, async stop() {} }),
  });
  assert.equal(pool.listenerCount('error'), 1);
  const logged = [];
  const original = console.error;
  console.error = (...args) => logged.push(args.join(' '));
  try {
    pool.emit('error', new Error('terminating connection due to administrator command; password=hunter2'));
  } finally {
    console.error = original;
  }
  assert.deepEqual(logged, ['MCP task database connection unavailable']);
  await service.close();
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
  assert.equal(publishedSettings.observer, false);
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

test('an external observer needs no payload key and never initializes schema or workers', async () => {
  let publishedSettings;
  let reads = 0;
  const env = { ...baseEnv, MCP_TASK_NAMESPACE: 'storefront-prod', MCP_TASKS_SCALING_APP_SLUG: 'storefront-worker', MCP_TASKS_SCALING_TOKEN: 'metrics-secret' };
  delete env.MCP_TASK_OWNER_KEY;
  const service = await startMcpTaskRuntime({ ...config, tasks: { ...config.tasks, max_attempts: 5 } }, {
    env, role: 'observer',
    checkSchema: async () => {},
    createPool: () => ({ async end() {} }),
    createStore() { assert.fail('observer must not access encrypted task store'); },
    createRuntime() { assert.fail('observer must not initialize a worker'); },
    createObserver: options => {
      assert.equal(options.maxAttempts, 5);
      return { async queueMetrics() { reads++; return { outstandingTasks: 2, oldestAgeSeconds: 1 }; } };
    },
    createMetricsPublisher(options) { publishedSettings = options; return { async close() {} }; },
  });
  assert.equal(reads, 1);
  assert.equal(publishedSettings.keepAlive, true);
  assert.equal(service.taskRuntime, undefined);
  await service.close();
});
