const DEFAULT_API_URL = 'https://api.gregale.dev';
const METRIC_NAMES = Object.freeze({
  outstanding: 'mcp_tasks_outstanding',
  activeWorkers: 'mcp_tasks_active_workers',
  drainingWorkers: 'mcp_tasks_draining_workers',
  unsupportedHandlers: 'mcp_tasks_unsupported_handler_tasks',
  observerHeartbeat: 'mcp_tasks_observer_heartbeat',
  running: 'mcp_tasks_running',
  failed: 'mcp_tasks_failed',
  retryWaiting: 'mcp_tasks_retry_waiting',
  capacityWaiting: 'mcp_tasks_capacity_waiting',
  oldestAge: 'mcp_tasks_oldest_age_seconds',
});
const PUSH_TIMEOUT_MS = 5000;

function safeAPIURL(value) {
  let url;
  try { url = new URL(value); } catch { throw new Error('Invalid Gregale metrics API URL'); }
  if (url.protocol !== 'https:' || !url.hostname || url.username || url.password || url.search || url.hash) {
    throw new Error('Gregale metrics API URL must be HTTPS without credentials, query, or fragment');
  }
  return url.origin;
}

export function resolveMcpTaskMetricsSettings({ env = process.env, role = 'combined' } = {}) {
  const appSlug = typeof env.MCP_TASKS_SCALING_APP_SLUG === 'string' ? env.MCP_TASKS_SCALING_APP_SLUG.trim() : '';
  const token = typeof env.MCP_TASKS_SCALING_TOKEN === 'string' ? env.MCP_TASKS_SCALING_TOKEN.trim() : '';
  if (!appSlug && !token) return undefined;
  if (!appSlug || !token) throw new Error('MCP task scaling requires both MCP_TASKS_SCALING_APP_SLUG and MCP_TASKS_SCALING_TOKEN');
  if (role !== 'worker' && role !== 'observer') throw new Error('MCP task scaling metrics can only be published by the dedicated worker role or observer');
  if (!/^[a-z0-9][a-z0-9-]{0,62}$/.test(appSlug)) throw new Error('Invalid MCP task scaling app slug');
  const configuredURL = typeof env.GREGALE_API_URL === 'string' && env.GREGALE_API_URL.trim() ? env.GREGALE_API_URL.trim() : DEFAULT_API_URL;
  return { appSlug, token, apiURL: safeAPIURL(configuredURL) };
}

export function startMcpTaskMetricsPublisher({ store, appSlug, token, apiURL = DEFAULT_API_URL, fetchImpl = globalThis.fetch, intervalMs = 15_000, role = 'worker', keepAlive = false, onError = () => {} }) {
  if (!store || typeof store.queueMetrics !== 'function') throw new Error('MCP task metrics require a task store queueMetrics method');
  if (typeof appSlug !== 'string' || !/^[a-z0-9][a-z0-9-]{0,62}$/.test(appSlug)) throw new Error('Invalid MCP task scaling app slug');
  if (typeof token !== 'string' || !token.trim()) throw new Error('MCP task scaling token is required');
  if (typeof fetchImpl !== 'function') throw new Error('MCP task metrics require fetch');
  if (!Number.isSafeInteger(intervalMs) || intervalMs < 5000 || intervalMs > 300_000) throw new Error('MCP task metrics interval must be between 5000 and 300000 milliseconds');
  if (role !== 'worker' && role !== 'observer') throw new Error('Invalid MCP task metrics publisher role');
  const baseURL = safeAPIURL(apiURL);
  const endpoint = name => new URL(`/v1/apps/${encodeURIComponent(appSlug)}/custom-metrics/${name}`, baseURL).href;
  let stopped = false;
  let publishing;

  async function push(name, value) {
    const response = await fetchImpl(endpoint(name), {
      method: 'PUT',
      redirect: 'error',
      signal: AbortSignal.timeout(PUSH_TIMEOUT_MS),
      headers: { authorization: `Bearer ${token}`, 'content-type': 'application/json' },
      body: JSON.stringify({ value }),
    });
    if (response.status !== 204) throw new Error(`Gregale custom metric push failed with HTTP ${response.status}`);
  }

  function publish() {
    if (stopped || publishing) return publishing;
    publishing = (async () => {
      const metrics = await store.queueMetrics();
      if (!metrics || !Number.isSafeInteger(metrics.outstandingTasks) || metrics.outstandingTasks < 0 ||
          !['runningTasks', 'capacityWaitingTasks', 'failedTasks', 'retryWaitingTasks', 'activeWorkers', 'drainingWorkers', 'unsupportedHandlerTasks'].every(key => Number.isSafeInteger(metrics[key]) && metrics[key] >= 0) ||
          !Number.isFinite(metrics.oldestAgeSeconds) || metrics.oldestAgeSeconds < 0) {
        throw new Error('MCP task queue metrics returned invalid values');
      }
      await Promise.all([
        push(METRIC_NAMES.outstanding, metrics.outstandingTasks),
        push(METRIC_NAMES.activeWorkers, metrics.activeWorkers),
        push(METRIC_NAMES.drainingWorkers, metrics.drainingWorkers),
        push(METRIC_NAMES.unsupportedHandlers, metrics.unsupportedHandlerTasks),
        push(METRIC_NAMES.running, metrics.runningTasks),
        push(METRIC_NAMES.failed, metrics.failedTasks),
        push(METRIC_NAMES.retryWaiting, metrics.retryWaitingTasks),
        push(METRIC_NAMES.capacityWaiting, metrics.capacityWaitingTasks),
        push(METRIC_NAMES.oldestAge, Math.min(metrics.oldestAgeSeconds, 1_000_000_000_000)),
      ]);
      // A worker must never impersonate the separate always-on observer.
      if (role === 'observer') await push(METRIC_NAMES.observerHeartbeat, 1);
    })().catch(() => {
      try { onError(); } catch { /* Metrics reporting must not stop task execution. */ }
    }).finally(() => { publishing = undefined; });
    return publishing;
  }

  // Publish immediately, then keep the gauge fresh on a bounded cadence. All
  // worker replicas may publish the same aggregate safely; the API upserts one
  // app-scoped gauge and never stores task or customer identifiers.
  void publish();
  const timer = setInterval(() => { void publish(); }, intervalMs);
  if (!keepAlive) timer.unref?.();

  return {
    async close() {
      if (stopped) return;
      stopped = true;
      clearInterval(timer);
      await publishing;
    },
  };
}
