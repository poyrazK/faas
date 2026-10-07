import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {resolve} from 'node:path';
import {createServer} from 'node:http';
import {once} from 'node:events';
import {AppsService, FaaSClient, type RouteMonitorIncident, type RouteMonitorConfig} from '../src/index.js';

test('production route monitor preserves budgets, incident snapshots and pagination', async (t) => {
  const incident: RouteMonitorIncident = JSON.parse(readFileSync(resolve('../../tests/fixtures/production-route-incident.json'), 'utf8'));
  const config: RouteMonitorConfig = {app_id: incident.app_id, enabled: true, revision: 1, routes: [{method: 'POST', path: '/checkout', max_5xx_rate_bps: 0, max_p95_ms: 300}]};
  let calls = 0;
  const server = createServer(async (req, res) => {
    calls++;
    const url = new URL(req.url!, 'http://localhost');
    res.setHeader('Content-Type', 'application/json');
    if (req.method === 'PUT') {
      assert.equal(url.pathname, '/v1/apps/demo/route-monitor');
      const chunks = []; for await (const chunk of req) chunks.push(chunk);
      assert.deepEqual(JSON.parse(Buffer.concat(chunks).toString()), {enabled: true, expected_revision: 0, routes: config.routes});
      res.end(JSON.stringify(config));
    } else if (url.pathname.endsWith('/report')) res.end(JSON.stringify(incident.opening_report));
    else if (url.pathname.endsWith(`/incidents/${incident.id}`)) res.end(JSON.stringify(incident));
    else if (url.pathname.endsWith('/incidents')) {
      assert.equal(url.searchParams.get('limit'), '5'); assert.equal(url.searchParams.get('before'), incident.id);
      res.end(JSON.stringify({app_id: incident.app_id, incidents: [incident]}));
    } else { assert.equal(url.pathname, '/v1/apps/demo/route-monitor'); res.end(JSON.stringify(config)); }
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, {token: 'test', retry: {maxAttempts: 1, backoffMs: 0}});
  assert.deepEqual(await AppsService.getRouteMonitor({slug: 'demo'}), config);
  assert.deepEqual(await AppsService.setRouteMonitor({slug: 'demo', requestBody: {enabled: true, expected_revision: 0, routes: config.routes}}), config);
  assert.deepEqual(await AppsService.getRouteMonitorReport({slug: 'demo'}), incident.opening_report);
  assert.deepEqual(await AppsService.listRouteMonitorIncidents({slug: 'demo', limit: 5, before: incident.id}), {app_id: incident.app_id, incidents: [incident]});
  assert.deepEqual(await AppsService.getRouteMonitorIncident({slug: 'demo', incident: incident.id}), incident);
  assert.equal(calls, 5);
});
