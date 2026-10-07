import pg from 'pg';
import { createPostgresMcpTaskStore } from './task-store.js';
import { createMcpTaskRuntime, mcpTaskHandlers } from './tasks.js';
import { resolveMcpTaskMetricsSettings, startMcpTaskMetricsPublisher } from './task-metrics.js';

function envName(value) {
  if (typeof value !== 'string' || !/^[A-Z_][A-Z0-9_]*$/.test(value)) {
    throw new Error('Invalid MCP task environment variable name');
  }
  return value;
}

export function resolveMcpTaskSettings(config, { env = process.env, role = 'combined' } = {}) {
  if (!['combined', 'web', 'worker'].includes(role)) throw new Error('MCP task role must be combined, web, or worker');
  const settings = config?.tasks;
  if (settings?.enabled !== true) {
    if (role === 'worker') throw new Error('The MCP Tasks worker requires tasks.enabled=true');
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
  if (!ownerKey) throw new Error('MCP Tasks are enabled but the configured owner key is unavailable');
  if (typeof namespace !== 'string' || !namespace.trim()) throw new Error('MCP Tasks require a stable namespace');

  return {
    enabled: true,
    role,
    databaseURL,
    ownerKey,
    namespace: namespace.trim(),
    ttlMs: (settings.ttl_seconds ?? 86400) * 1000,
    pollIntervalMs: settings.poll_interval_ms ?? 2000,
    workerConcurrency: settings.worker_concurrency ?? 1,
  };
}

export async function startMcpTaskRuntime(config, {
  env = process.env,
  role = 'combined',
  createPool = options => new pg.Pool(options),
  createStore = createPostgresMcpTaskStore,
  createRuntime = createMcpTaskRuntime,
  createMetricsPublisher = startMcpTaskMetricsPublisher,
  handlers = mcpTaskHandlers,
} = {}) {
  const settings = resolveMcpTaskSettings(config, { env, role });
  if (!settings.enabled) return { taskRuntime: undefined, async close() {} };
  const metricsSettings = resolveMcpTaskMetricsSettings({ env, role: settings.role });

  const pool = createPool({ connectionString: settings.databaseURL, max: 4, application_name: 'gregale-mcp-tasks' });
  let taskRuntime;
  let metricsPublisher;
  try {
    const store = createStore({ pool, namespace: settings.namespace, ownerKey: settings.ownerKey, ttlMs: settings.ttlMs });
    taskRuntime = createRuntime({
      store,
      handlers,
      pollIntervalMs: settings.pollIntervalMs,
      workerConcurrency: settings.workerConcurrency,
      workerEnabled: settings.role !== 'web',
      keepAlive: settings.role === 'worker',
      onError: () => console.error(JSON.stringify({ event: 'mcp_task_runtime_error' })),
    });
    await taskRuntime.start();
    if (metricsSettings) metricsPublisher = createMetricsPublisher({ store, ...metricsSettings });
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
          await metricsPublisher?.close();
          await taskRuntime.stop();
        } finally {
          await pool.end();
        }
      })();
      return closing;
    },
  };
}
