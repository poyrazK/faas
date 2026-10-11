import test from 'node:test';
import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import {once} from 'node:events';
import {AppsService, FaaSClient, type ProfileInvestigationResponse, type SaveProfileInvestigationRequest} from '../src/index.js';

test('saved profiling investigation operations preserve revision, path and expired notes', async (t) => {
  const query = {deployment_id: '11111111-1111-4111-8111-111111111111', runtime: 'node24', start: '2026-10-07T11:00:00.123Z', end: '2026-10-07T12:00:00.123Z'};
  const payload: ProfileInvestigationResponse = {
    saved: {id: '33333333-3333-4333-8333-333333333333', app_id: '44444444-4444-4444-8444-444444444444', revision: 3, created_at: query.end, updated_at: query.end,
      investigation: {title: 'Regression', findings: 'parseJSON', notes: 'Saved notes', baseline: query, candidate: query, selected_path: {view: 'comparison', frames: [{name: 'all'}, {name: 'parseJSON', file: 'app.js', line: 42}]}}},
    url: '/dashboard/apps/demo/profiles?investigation_id=33333333-3333-4333-8333-333333333333',
    baseline_status: {status: 'expired', detail: 'Notes remain available'}, candidate_status: {status: 'retained', detail: 'Samples may be absent'},
  };
  const methods: string[] = [];
  const server = createServer((req, res) => {
    const url = new URL(req.url!, 'http://localhost');
    assert.equal(req.headers.authorization, 'Bearer test-token');
    assert.ok(url.pathname.startsWith('/v1/apps/demo/profiles/investigations'));
    methods.push(req.method!);
    let body = '';
    req.on('data', part => {body += part});
    req.on('end', () => {
      if (req.method === 'DELETE') {
        assert.equal(url.searchParams.get('expected_revision'), '3');
        res.writeHead(204); res.end(); return;
      }
      if (url.pathname.endsWith('/check')) {
        assert.deepEqual(JSON.parse(body), {expected_revision: 3});
        const assessed: ProfileInvestigationResponse = {...payload, saved: {...payload.saved, revision: 4, assessment: {investigation_revision: 4, checked_at: query.end, status: 'inconclusive', reason: 'History expired', options: {relative_increase_percent: 20, absolute_increase_cpu_per_second: .01, minimum_profiles: 3, minimum_coverage_ratio: .8}, baseline: query, candidate: query, evidence: [], uncomparable_entries: 0}}};
        res.setHeader('Content-Type', 'application/json'); res.writeHead(200); res.end(JSON.stringify(assessed)); return;
      }
      if (req.method === 'POST' || req.method === 'PUT') {
        const input: SaveProfileInvestigationRequest = JSON.parse(body);
        assert.equal(input.expected_revision, req.method === 'POST' ? 0 : 3);
        assert.deepEqual(input.investigation, payload.saved.investigation);
      }
      res.setHeader('Content-Type', 'application/json');
      res.writeHead(req.method === 'POST' ? 201 : 200);
      res.end(JSON.stringify(url.pathname.endsWith('/investigations') && req.method === 'GET' ? {investigations: [payload]} : payload));
    });
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, {token: 'test-token', retry: {maxAttempts: 1, backoffMs: 0}});
  assert.deepEqual(await AppsService.listProfileInvestigations({slug: 'demo'}), {investigations: [payload]});
  assert.deepEqual(await AppsService.getProfileInvestigation({slug: 'demo', id: payload.saved.id}), payload);
  assert.deepEqual(await AppsService.createProfileInvestigation({slug: 'demo', requestBody: {expected_revision: 0, investigation: payload.saved.investigation}}), payload);
  assert.deepEqual(await AppsService.updateProfileInvestigation({slug: 'demo', id: payload.saved.id, requestBody: {expected_revision: 3, investigation: payload.saved.investigation}}), payload);
  await AppsService.deleteProfileInvestigation({slug: 'demo', id: payload.saved.id, expectedRevision: 3});
  const checked = await AppsService.checkProfileRegression({slug: 'demo', id: payload.saved.id, requestBody: {expected_revision: 3}});
  assert.equal(checked.saved.revision, 4);
  assert.equal(checked.saved.assessment?.status, 'inconclusive');
  assert.equal(checked.saved.assessment?.options.minimum_coverage_ratio, .8);
  assert.deepEqual(methods, ['GET', 'GET', 'POST', 'PUT', 'DELETE', 'POST']);
});
