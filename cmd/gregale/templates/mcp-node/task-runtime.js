import pg from 'pg';
import { createPostgresMcpTaskStore, createMcpTaskQueueObserver } from './task-store.js';
import { createMcpTaskRuntime, mcpTaskHandlers } from './tasks.js';
import './task-handlers.js';
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
  const drainSeconds = Number(env.MCP_DRAIN_TIMEOUT_SECONDS ?? 10);
  if (!Number.isInteger(drainSeconds) || drainSeconds < 1 || drainSeconds > 300) throw new Error('MCP_DRAIN_TIMEOUT_SECONDS must be an integer between 1 and 300');
  const settings = config?.tasks;
  if (settings?.enabled !== true) {
    if (role === 'worker' || role === 'observer') throw new Error('The MCP Tasks worker or observer requires tasks.enabled=true');
    return { enabled: false, role };
  }

  const databaseURL = env[envName(settings.database_url_env)];
  const ownerKey = env[envName(settings.owner_key_env)];
  const ownerKeyID = settings.owner_key_id ?? 'default';
  const keyIDPattern = /^[A-Za-z0-9._-]{1,64}$/;
  if (typeof ownerKeyID !== 'string' || !keyIDPattern.test(ownerKeyID)) throw new Error('Invalid MCP task owner key ID');
  const previousKeySettings = settings.previous_owner_keys ?? [];
  if (!Array.isArray(previousKeySettings) || previousKeySettings.length > 16) throw new Error('MCP task previous_owner_keys must contain at most 16 keys');
  const previousOwnerKeys = previousKeySettings.map(previous => {
    if (!previous || typeof previous !== 'object' || typeof previous.id !== 'string' || !keyIDPattern.test(previous.id)) {
      throw new Error('Invalid MCP task previous owner key configuration');
    }
    return { id: previous.id, key: env[envName(previous.env)] };
  });
  if (new Set([ownerKeyID, ...previousOwnerKeys.map(key => key.id)]).size !== previousOwnerKeys.length + 1) {
    throw new Error('MCP task owner key IDs must be unique');
  }
  const payloadEncryptionVersion = settings.payload_encryption_version ?? 1;
  if (payloadEncryptionVersion !== 1 && payloadEncryptionVersion !== 2) throw new Error('MCP task payload encryption version must be 1 or 2');
  const namespaceOverride = typeof env.MCP_TASK_NAMESPACE === 'string' ? env.MCP_TASK_NAMESPACE.trim() : '';
  if (role !== 'combined' && !namespaceOverride) {
    throw new Error(`MCP Tasks role ${role} requires MCP_TASK_NAMESPACE to match every app sharing the queue`);
  }
  const configuredNamespace = settings.namespace_env ? env[envName(settings.namespace_env)] : '';
  const namespace = namespaceOverride || configuredNamespace;
  if (!databaseURL) throw new Error('MCP Tasks are enabled but the configured PostgreSQL binding is unavailable');
  if (!ownerKey && role !== 'observer') throw new Error('MCP Tasks are enabled but the configured owner key is unavailable');
  if (role !== 'observer' && previousOwnerKeys.some(key => !key.key || Buffer.byteLength(key.key) < 32)) {
    throw new Error('MCP Tasks are enabled but a configured previous owner key is unavailable or too short');
  }
  if (typeof namespace !== 'string' || !namespace.trim()) throw new Error('MCP Tasks require a stable namespace');

  return {
    enabled: true,
    role,
    databaseURL,
    ownerKey,
    ownerKeyID,
    previousOwnerKeys,
    payloadEncryptionVersion,
    namespace: namespace.trim(),
    drainTimeoutMs: drainSeconds * 1000,
    deploymentID: typeof env.FAAS_DEPLOYMENT_ID === 'string' ? env.FAAS_DEPLOYMENT_ID.trim() : '',
    ttlMs: (settings.ttl_seconds ?? 86400) * 1000,
    pollIntervalMs: settings.poll_interval_ms ?? 1000,
    workerConcurrency: settings.worker_concurrency ?? 1,
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
  let rewrapTimer;
  let store;
  try {
    store = role === 'observer' ? createObserver({ pool, namespace: settings.namespace }) : createStore({ pool, namespace: settings.namespace, ownerKey: settings.ownerKey, ownerKeyID: settings.ownerKeyID, previousOwnerKeys: settings.previousOwnerKeys, payloadEncryptionVersion: settings.payloadEncryptionVersion, ttlMs: settings.ttlMs, maxOutstanding: settings.maxOutstanding, maxOutstandingPerOwner: settings.maxOutstandingPerOwner });
    if (role === 'observer') {
      try {
        await store.queueMetrics();
      } catch {
        console.error(JSON.stringify({ event: 'mcp_task_observer_health_check_failed' }));
        throw new Error('MCP task observer health check failed');
      }
    } else {
      taskRuntime = createRuntime({
        store,
        handlers,
        deploymentID: settings.deploymentID,
        drainTimeoutMs: settings.drainTimeoutMs,
        onDrain: progress => console.log(JSON.stringify({ event: 'mcp_task_drain', ...progress })),
        pollIntervalMs: settings.pollIntervalMs,
        workerConcurrency: settings.workerConcurrency,
        workerEnabled: settings.role !== 'web',
        keepAlive: settings.role === 'worker',
        onError: () => console.error(JSON.stringify({ event: 'mcp_task_runtime_error' })),
      });
    }
    await taskRuntime?.start();
    if (role !== 'observer' && settings.payloadEncryptionVersion === 2 && typeof store.rewrapBatch === 'function') {
      const rewrap = () => store.rewrapBatch().catch(() => console.error(JSON.stringify({ event: 'mcp_task_key_rewrap_failed' })));
      void rewrap();
      rewrapTimer = setInterval(rewrap, 30_000);
      rewrapTimer.unref?.();
    }
    if (metricsSettings) metricsPublisher = createMetricsPublisher({ store, ...metricsSettings,
      keepAlive: role === 'observer',
      observer: role === 'observer',
      onError: () => console.error(JSON.stringify({ event: role === 'observer' ? 'mcp_task_observer_health_check_failed' : 'mcp_task_metrics_publish_failed' })),
    });
  } catch {
    clearInterval(rewrapTimer);
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
          clearInterval(rewrapTimer);
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
