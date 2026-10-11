import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {resolve} from 'node:path';
import {createServer} from 'node:http';
import {once} from 'node:events';
import {AppsService, FaaSClient, type RouteMonitorIncident, type RouteMonitorConfig, type RouteMonitorPreview, type RouteMonitorWebhookPayload} from '../src/index.js';

test('route monitor escalation webhook carries a stable aggregate transition summary', () => {
  const event: RouteMonitorWebhookPayload = {
    version: 1,
    app_id: '11111111-1111-4111-8111-111111111111',
    deployment_id: '22222222-2222-4222-8222-222222222222',
    incident_id: '33333333-3333-4333-8333-333333333333',
    transition_id: '44444444-4444-4444-8444-444444444444',
    revision: 7,
    status: 'open',
    checked_at: '2026-10-06T09:01:00Z',
    incident_path: '/v1/apps/demo/route-monitor/incidents/33333333-3333-4333-8333-333333333333',
    customer_impact: {group_by: 'tenant', coverage: 'observed_only', observed_customers: 5, violated_customers: 2, unknown_customers: 1},
    escalation: {previous_checked_at: '2026-10-06T09:00:00Z', newly_violated_routes: 1, newly_violated_signals: 2},
  };
  assert.equal(event.escalation?.newly_violated_signals, 2);
  assert.equal(event.customer_impact?.unknown_customers, 1);
  assert.equal('customer_id' in event, false);
});

test('production route monitor preserves budgets, incident snapshots and pagination', async (t) => {
  const incident: RouteMonitorIncident = JSON.parse(readFileSync(resolve('../../tests/fixtures/production-route-incident.json'), 'utf8'));
  incident.baseline = {deployment_id: '66666666-6666-4666-8666-666666666666', commit_sha: 'b'.repeat(40), repository: 'github.com/team/service', source_root: '.'};
  incident.timeline = [{
    checked_at: incident.opened_at,
    coverage: 'observed_only',
    status: 'violated',
    reason: 'sustained_budget_violation',
    customer_impact: {group_by: 'tenant', coverage: 'observed_only', observed_customers: 5, violated_customers: 2, unknown_customers: 1},
    routes: [{
      route_index: 0,
      status: 'violated',
      error_status: 'violated',
      latency_status: 'healthy',
      customer_impact: {group_by: 'tenant', coverage: 'observed_only', observed_customers: 3, violated_customers: 2, unknown_customers: 1},
    }],
  }];
  incident.timeline_truncated = false;
  incident.escalations = [{
    transition_id: '44444444-4444-4444-8444-444444444444',
    checked_at: '2026-10-03T13:05:04.746212+00:00',
    previous_checked_at: '2026-10-03T13:04:04.746212+00:00',
    newly_violated_routes: 1,
    newly_violated_signals: 1,
    signals: [{route_index: 0, signal: 'latency', finding: incident.opening_report.routes[0]!}],
    evidence: incident.evidence.slice(0, 1),
    evidence_truncated: false,
  }];
  incident.escalations_truncated = false;
  const config: RouteMonitorConfig = {app_id: incident.app_id, enabled: true, revision: 1, routes: [{method: 'POST', path: '/checkout', max_5xx_rate_bps: 0, max_p95_ms: 300}]};
  const preview: RouteMonitorPreview = {current_revision: 1, preview_only: true, config_change_resets_observation_anchor: true, report: incident.opening_report};
  let calls = 0;
  const server = createServer(async (req, res) => {
    calls++;
    const url = new URL(req.url!, 'http://localhost');
    res.setHeader('Content-Type', 'application/json');
    if (req.method === 'POST') {
      assert.equal(url.pathname, '/v1/apps/demo/route-monitor/preview');
      const chunks = []; for await (const chunk of req) chunks.push(chunk);
      assert.deepEqual(JSON.parse(Buffer.concat(chunks).toString()), {routes: config.routes});
      res.end(JSON.stringify(preview));
    } else if (req.method === 'PUT') {
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
  assert.deepEqual(await AppsService.previewRouteMonitor({slug: 'demo', requestBody: {routes: config.routes}}), preview);
  assert.deepEqual(await AppsService.getRouteMonitorReport({slug: 'demo'}), incident.opening_report);
  assert.deepEqual(await AppsService.listRouteMonitorIncidents({slug: 'demo', limit: 5, before: incident.id}), {app_id: incident.app_id, incidents: [incident]});
  assert.deepEqual(await AppsService.getRouteMonitorIncident({slug: 'demo', incident: incident.id}), incident);
  assert.equal(calls, 6);
});
