import { createServer } from 'node:http';
import { formatEventAcknowledgmentPreparationMetrics,
  EVENT_ACKNOWLEDGMENT_PREPARATION_PROMETHEUS_CONTENT_TYPE } from '@gregale/sdk-node';

/** Application-owned example. Bind to loopback as documented; never attach to delivery ingress. */
export function createPreparationMetricsServer(metrics) {
  return createServer((req, res) => {
    res.setHeader('cache-control', 'no-store');
    if (req.url !== '/metrics') {
      res.writeHead(404, { 'content-type': 'text/plain; charset=utf-8' });
      res.end('not found\n');
      return;
    }
    if (req.method !== 'GET') {
      res.writeHead(405, { allow: 'GET', 'content-type': 'text/plain; charset=utf-8' });
      res.end('method not allowed\n');
      return;
    }
    let body;
    try {
      body = formatEventAcknowledgmentPreparationMetrics(metrics.snapshot());
    } catch {
      res.writeHead(503, { 'content-type': 'text/plain; charset=utf-8' });
      res.end('metrics unavailable\n');
      return;
    }
    res.writeHead(200, { 'content-type': EVENT_ACKNOWLEDGMENT_PREPARATION_PROMETHEUS_CONTENT_TYPE });
    res.end(body);
  });
}
