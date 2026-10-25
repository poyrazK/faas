import { createServer } from 'node:http';
import { performance } from 'node:perf_hooks';
import { EventAcknowledgmentIntentHealthMetrics, formatEventAcknowledgmentIntentHealthMetrics,
  EVENT_ACKNOWLEDGMENT_INTENT_HEALTH_PROMETHEUS_CONTENT_TYPE, getEventAcknowledgmentIntentHealth } from '@gregale/sdk-node/preview';

function wait(signal) {
  signal.throwIfAborted();
  return new Promise((resolve, reject) => {
    const finish = () => { signal.removeEventListener('abort', abort); resolve(); };
    const timer = setTimeout(finish, 5000);
    const abort = () => { clearTimeout(timer); signal.removeEventListener('abort', abort); reject(signal.reason); };
    signal.addEventListener('abort', abort, { once: true });
  });
}
/** One explicitly started poller and a separately bound loopback scrape server.
 * The host bounds pool acquisition, SQL, connection release and HTTP shutdown. */
export function createRecoveryHealthMetricsHost(pool, accountId, appId) {
  if (!pool || typeof pool.connect !== 'function') throw new TypeError('recovery metrics pool required');
  const metrics = new EventAcknowledgmentIntentHealthMetrics(accountId, appId, { maxAgeMs: 30000 });
  let running = false;
  const server = createServer((req, res) => {
    res.setHeader('cache-control', 'no-store');
    if (req.url !== '/metrics') { res.writeHead(404); res.end('not found\n'); return; }
    if (req.method !== 'GET') { res.writeHead(405, { allow: 'GET' }); res.end('method not allowed\n'); return; }
    let body;
    try { body = formatEventAcknowledgmentIntentHealthMetrics(metrics.snapshot()); }
    catch { res.writeHead(503); res.end('metrics unavailable\n'); return; }
    res.writeHead(200, { 'content-type': EVENT_ACKNOWLEDGMENT_INTENT_HEALTH_PROMETHEUS_CONTENT_TYPE }); res.end(body);
  });
  async function run(signal) {
    if (!(signal instanceof AbortSignal) || running) throw new TypeError('one recovery metrics poller and a host signal required');
    signal.throwIfAborted(); running = true;
    try {
      for (;;) {
        signal.throwIfAborted();
        const controller = new AbortController(), abort = () => controller.abort();
        const started = performance.now();
        signal.addEventListener('abort', abort, { once: true });
        const timer = setTimeout(abort, 5000);
        try {
          const health = await getEventAcknowledgmentIntentHealth(pool, accountId, appId, {
            scanLimit: 1000, stallAfterMs: 300000, signal: controller.signal,
          });
          signal.throwIfAborted();
          if (controller.signal.aborted || performance.now() - started >= 5000) metrics.recordReadFailure();
          else metrics.record(health);
        } catch { signal.throwIfAborted(); metrics.recordReadFailure(); }
        finally { clearTimeout(timer); signal.removeEventListener('abort', abort); controller.abort(); }
        await wait(signal); // Active acquisition/query/release always finishes before another read.
      }
    } finally { metrics.recordReadFailure(); running = false; }
  }
  return Object.freeze({ metrics, server, run });
}
