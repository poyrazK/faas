import pg from 'pg';
import { checkMcpTaskSchema } from './task-schema.js';
import { parseMcpTaskEncryptionKeys } from './task-crypto.js';
import { createPostgresMcpTaskStore, createMcpTaskQueueObserver } from './task-store.js';
import { createMcpTaskRuntime, mcpTaskHandlers, validateMcpTaskRetryPolicy } from './tasks.js';
import './task-handlers.js';
import { resolveMcpTaskMetricsSettings, startMcpTaskMetricsPublisher } from './task-metrics.js';
import defaults from './task-limits.json' with { type: 'json' };

function envName(value) {
  if (typeof value !== 'string' || !/^[A-Z_][A-Z0-9_]*$/.test(value)) {
    throw new Error('Invalid MCP task environment variable name');
  }
  return value;
}

// The shared CLI config represents omitted numeric settings as zero. Preserve
// that defaulting contract without treating booleans or numeric strings as zero.
function taskDefault(value, fallback) {
  return value === undefined || value === null || value === 0 ? fallback : value;
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

  const nativeOwnerKeys = role === 'observer' ? [] : [{ id: settings.owner_key_id ?? 'default', key: ownerKey }];
  if (role !== 'observer') {
    const previous = settings.previous_owner_keys ?? [];
    if (!Array.isArray(previous) || previous.length > 16) throw new Error('Invalid MCP task previous owner keys');
    for (const item of previous) {
      if (!item || typeof item !== 'object') throw new Error('Invalid MCP task previous owner keys');
      nativeOwnerKeys.push({ id: item.id, key: env[envName(item.env)] });
    }
    const ids = new Set();
    for (const item of nativeOwnerKeys) {
      if (typeof item.id !== 'string' || !/^[A-Za-z0-9._-]{1,64}$/.test(item.id) || ids.has(item.id) ||
          typeof item.key !== 'string' || Buffer.byteLength(item.key) < 32) throw new Error('Invalid MCP task native owner key configuration');
      ids.add(item.id);
    }
  }
  return {
    enabled: true,
    nativeOwnerKeys,
    role,
    databaseURL,
    ownerKey,
    encryptionKeys: role !== 'observer' && settings.encryption_keys_env ? parseMcpTaskEncryptionKeys(env[envName(settings.encryption_keys_env)]) : undefined,
    namespace: namespace.trim(),
    ttlMs: taskDefault(settings.ttl_seconds, 86400) * 1000,
    pollIntervalMs: taskDefault(settings.poll_interval_ms, 2000),
    workerConcurrency: taskDefault(settings.worker_concurrency, 1),
    deploymentID: (env.FAAS_DEPLOYMENT_ID || '').trim(),
    shutdownTimeoutMs: env.MCP_DRAIN_TIMEOUT_SECONDS === undefined ? taskDefault(settings.shutdown_timeout_ms, 10_000) : Number(env.MCP_DRAIN_TIMEOUT_SECONDS) * 1000,
    ...validateMcpTaskRetryPolicy({ maxAttempts: taskDefault(settings.max_attempts, 3), retryBaseDelayMs: taskDefault(settings.retry_base_delay_ms, 1000), retryMaxDelayMs: taskDefault(settings.retry_max_delay_ms, 60_000) }),
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
  checkSchema = checkMcpTaskSchema,
  createRuntime = createMcpTaskRuntime,
  createMetricsPublisher = startMcpTaskMetricsPublisher,
  handlers = mcpTaskHandlers,
} = {}) {
  const settings = resolveMcpTaskSettings(config, { env, role });
  if (!settings.enabled) return { taskRuntime: undefined, async close() {} };
  if (!Number.isInteger(settings.workerConcurrency) || settings.workerConcurrency < 1 || settings.workerConcurrency > 16) throw new Error('Invalid MCP task worker concurrency');
  const metricsSettings = resolveMcpTaskMetricsSettings({ env, role: settings.role });
  if (role === 'observer' && !metricsSettings) throw new Error('MCP task observer requires scaling metric credentials');

  // Reserve slots for LISTEN, polling, maintenance and foreground requests.
  const workerSlots = role === 'combined' || role === 'worker' ? settings.workerConcurrency : 0;
  const pool = createPool({ connectionString: settings.databaseURL, max: workerSlots + 4, connectionTimeoutMillis: 5000, statement_timeout: 10_000, application_name: 'gregale-mcp-tasks' });
  // An idle client dropped by a database restart must not crash the runtime;
  // driver errors can carry connection details, so report a fixed message.
  pool.on?.('error', () => console.error('MCP task database connection unavailable'));
  let taskRuntime;
  let metricsPublisher;
  try {
    const store = role === 'observer' ? createObserver({ pool, namespace: settings.namespace, maxRunning: settings.maxRunning, maxRunningPerOwner: settings.maxRunningPerOwner, maxAttempts: settings.maxAttempts, compatibility: true }) : createStore({ pool, namespace: settings.namespace, ownerKey: settings.ownerKey, encryptionKeys: settings.encryptionKeys, nativeOwnerKeys: settings.nativeOwnerKeys, ttlMs: settings.ttlMs, maxRunning: settings.maxRunning, maxRunningPerOwner: settings.maxRunningPerOwner, maxOutstanding: settings.maxOutstanding, maxOutstandingPerOwner: settings.maxOutstandingPerOwner, maxAttempts: settings.maxAttempts });
    if (role === 'observer') {
      await checkSchema({ pool, namespace: settings.namespace, role: 'observer' });
      await store.queueMetrics();
    }
    else taskRuntime = createRuntime({
      store,
      handlers,
      pollIntervalMs: settings.pollIntervalMs,
      workerConcurrency: settings.workerConcurrency,
      shutdownTimeoutMs: settings.shutdownTimeoutMs,
      deploymentID: settings.deploymentID,
      onDrain: progress => console.log(JSON.stringify({event:'mcp_task_drain',...progress})),
      maxAttempts: settings.maxAttempts,
      retryBaseDelayMs: settings.retryBaseDelayMs,
      retryMaxDelayMs: settings.retryMaxDelayMs,
      workerEnabled: settings.role !== 'web',
      keepAlive: settings.role === 'worker',
      onError: () => console.error(JSON.stringify({ event: 'mcp_task_runtime_error' })),
    });
    await taskRuntime?.start();
    if (metricsSettings) metricsPublisher = createMetricsPublisher({ store, ...metricsSettings, role: settings.role, observer: role === 'observer',
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
