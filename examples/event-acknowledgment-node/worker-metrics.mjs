import {
  EventAcknowledgmentIntentRecoveryWorkerMetrics, recoverEventAcknowledgmentIntentsWithMetrics,
  formatEventAcknowledgmentIntentHealthMetrics, formatEventAcknowledgmentIntentRecoveryWorkerMetrics,
  EVENT_ACKNOWLEDGMENT_INTENT_HEALTH_PROMETHEUS_CONTENT_TYPE,
} from '@gregale/sdk-node/preview';
import { createRecoveryHealthMetricsHost } from './recovery-metrics.mjs';

/** One explicit same-loop pass observer, health poller and unbound scrape server.
 * Schedule recover() separately, process its original result/error, then decide
 * whether to schedule again. Construction and scraping start no recovery work. */
export function createRecoveryWorkerMetricsHost(pool, accountId, appId) {
  const health = createRecoveryHealthMetricsHost(pool, accountId, appId);
  const workerMetrics = new EventAcknowledgmentIntentRecoveryWorkerMetrics(accountId, appId, {
    maxIdleMs: 60000, maxPassDurationMs: 300000,
  });
  const server = health.server;
  // Replace this newly constructed server's sole health-only callback before
  // handing it to the application. No listener is bound and no user callback exists yet.
  server.removeAllListeners('request');
  server.on('request', (req, res) => {
    res.setHeader('cache-control', 'no-store');
    if (req.url !== '/metrics') { res.writeHead(404); res.end('not found\n'); return; }
    if (req.method !== 'GET') { res.writeHead(405, { allow: 'GET' }); res.end('method not allowed\n'); return; }
    let body;
    try {
      body = formatEventAcknowledgmentIntentHealthMetrics(health.metrics.snapshot())
        + formatEventAcknowledgmentIntentRecoveryWorkerMetrics(workerMetrics.snapshot());
    } catch { res.writeHead(503); res.end('metrics unavailable\n'); return; }
    res.writeHead(200, { 'content-type': EVENT_ACKNOWLEDGMENT_INTENT_HEALTH_PROMETHEUS_CONTENT_TYPE,
      'content-length': Buffer.byteLength(body) });
    res.end(body);
  });
  server.headersTimeout = 2000; server.requestTimeout = 5000;
  server.timeout = 5000; server.keepAliveTimeout = 5000; server.maxHeadersCount = 100;
  function recover(signal) {
    if (!(signal instanceof AbortSignal)) throw new TypeError('recovery host signal required');
    return recoverEventAcknowledgmentIntentsWithMetrics(pool, accountId, appId, workerMetrics, {
      signal, maxAttempts: 100, concurrency: 4, claimDurationMs: 30000,
      retryPolicy: { initialBackoffMs: 1000, maxBackoffMs: 300000 },
    });
  }
  return Object.freeze({ healthMetrics: health.metrics, workerMetrics, server, runHealth: health.run, recover });
}
