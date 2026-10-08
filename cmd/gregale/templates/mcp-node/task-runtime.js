import pg from 'pg';
import { parseMcpTaskEncryptionKeys } from './task-crypto.js';
import { createPostgresMcpTaskStore, createMcpTaskQueueObserver } from './task-store.js';
import { createMcpTaskRuntime, mcpTaskHandlers, validateMcpTaskRetryPolicy } from './tasks.js';
import { resolveMcpTaskMetricsSettings, startMcpTaskMetricsPublisher } from './task-metrics.js';
import defaults from './task-limits.json' with { type: 'json' };

function envName(value) {
  if (typeof value !== 'string' || !/^[A-Z_][A-Z0-9_]*$/.test(value)) {
    throw new Error('Invalid MCP task environment variable name');
  }
  return value;
}

export function resolveMcpTaskSettings(config, { env = process.env, role = 'combined' } = {}) {
  if (!['combined', 'web', 'worker', 'observer'].includes(role)) throw new Error('MCP task role must be combined, web, worker, or observer');
  const settings = config?.tasks;
  if (settings?.enabled !== true) {
    if (role === 'worker' || role === 'observer') throw new Error('The MCP Tasks worker or observer requires tasks.enabled=true');
    return { enabled: false, role };
  }

  const databaseURL = env[envName(settings.database_url_env)];
  const ownerKey = env[envName(settings.owner_key_env)];
  const namespaceOverride = typeof env.MCP_TASK_NAMESPACE === 'string' ? env.MCP_TASK_NAMESPACE.trim() : '';
  if (role !== 'combined' && !namespaceOverride) {
    throw new Error(`MCP Tasks role ${role} requires MCP_TASK_NAMESPACE to match every app sharing the queue`);
  }
  const configuredNamespace = settings.namespace_env ? env[envName(settings.namespace_env)] : '';
  const namespace = namespaceOverride || configuredNamespace;
  if (!databaseURL) throw new Error('MCP Tasks are enabled but the configured PostgreSQL binding is unavailable');
  if (!ownerKey && role !== 'observer') throw new Error('MCP Tasks are enabled but the configured owner key is unavailable');
  if (typeof namespace !== 'string' || !namespace.trim()) throw new Error('MCP Tasks require a stable namespace');

  return {
    enabled: true,
    role,
    databaseURL,
    ownerKey,
    encryptionKeys: role !== 'observer' && settings.encryption_keys_env ? parseMcpTaskEncryptionKeys(env[envName(settings.encryption_keys_env)]) : undefined,
    namespace: namespace.trim(),
    ttlMs: (settings.ttl_seconds ?? 86400) * 1000,
    pollIntervalMs: settings.poll_interval_ms ?? 2000,
    workerConcurrency: settings.worker_concurrency ?? 1,
    shutdownTimeoutMs: settings.shutdown_timeout_ms ?? 30_000,
    ...validateMcpTaskRetryPolicy({ maxAttempts: settings.max_attempts ?? 3, retryBaseDelayMs: settings.retry_base_delay_ms ?? 1000, retryMaxDelayMs: settings.retry_max_delay_ms ?? 60_000 }),
    maxRunning: settings.max_running || defaults.maxRunning,
    maxRunningPerOwner: settings.max_running_per_owner || defaults.maxRunningPerOwner,
    maxOutstanding: settings.max_outstanding || defaults.maxOutstanding,
    maxOutstandingPerOwner: settings.max_outstanding_per_owner || defaults.maxOutstandingPerOwner,
  };
}

export async function startMcpTaskRuntime(config, {
  env = process.env,
  role = 'combined',
  createPool = options => new pg.Pool(options),
  createStore = createPostgresMcpTaskStore,
  createObserver = createMcpTaskQueueObserver,
  createRuntime = createMcpTaskRuntime,
  createMetricsPublisher = startMcpTaskMetricsPublisher,
  handlers = mcpTaskHandlers,
} = {}) {
  const settings = resolveMcpTaskSettings(config, { env, role });
  if (!settings.enabled) return { taskRuntime: undefined, async close() {} };
  const metricsSettings = resolveMcpTaskMetricsSettings({ env, role: settings.role });
  if (role === 'observer' && !metricsSettings) throw new Error('MCP task observer requires scaling metric credentials');

  const pool = createPool({ connectionString: settings.databaseURL, max: 4, connectionTimeoutMillis: 5000, statement_timeout: 10_000, application_name: 'gregale-mcp-tasks' });
  let taskRuntime;
  let metricsPublisher;
  try {
    const store = role === 'observer' ? createObserver({ pool, namespace: settings.namespace, maxRunning: settings.maxRunning, maxRunningPerOwner: settings.maxRunningPerOwner }) : createStore({ pool, namespace: settings.namespace, ownerKey: settings.ownerKey, encryptionKeys: settings.encryptionKeys, ttlMs: settings.ttlMs, maxRunning: settings.maxRunning, maxRunningPerOwner: settings.maxRunningPerOwner, maxOutstanding: settings.maxOutstanding, maxOutstandingPerOwner: settings.maxOutstandingPerOwner });
    if (role === 'observer') await store.queueMetrics();
    else taskRuntime = createRuntime({
      store,
      handlers,
      pollIntervalMs: settings.pollIntervalMs,
      workerConcurrency: settings.workerConcurrency,
      shutdownTimeoutMs: settings.shutdownTimeoutMs,
      maxAttempts: settings.maxAttempts,
      retryBaseDelayMs: settings.retryBaseDelayMs,
      retryMaxDelayMs: settings.retryMaxDelayMs,
      workerEnabled: settings.role !== 'web',
      keepAlive: settings.role === 'worker',
      onError: () => console.error(JSON.stringify({ event: 'mcp_task_runtime_error' })),
    });
    await taskRuntime?.start();
    if (metricsSettings) metricsPublisher = createMetricsPublisher({ store, ...metricsSettings, role: settings.role,
      keepAlive: role === 'observer',
      onError: () => console.error(JSON.stringify({ event: 'mcp_task_metrics_publish_failed' })),
    });
  } catch {
    await metricsPublisher?.close().catch(() => {});
    await taskRuntime?.stop().catch(() => {});
    await pool.end().catch(() => {});
    throw new Error('Could not initialize the durable MCP task runtime');
  }

  let closing;
  return {
    taskRuntime,
    async close() {
      if (closing) return closing;
      closing = (async () => {
        try {
          await taskRuntime?.stop();
          await metricsPublisher?.close();
        } finally {
          await pool.end();
        }
      })();
      return closing;
    },
  };
}
