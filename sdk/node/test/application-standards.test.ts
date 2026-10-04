import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { FaaSClient, OrgsService } from '../src/index.js';

const id = '00000000-0000-4000-8000-000000000001';
const time = '2026-10-04T12:00:00Z';

test('standards SDK preserves explicit false previews, progress, expiry and history', async (t) => {
  const requests: Array<{ method: string | undefined; path: string | undefined; body: unknown; auth: string | undefined }> = [];
  const body = { assignment_id: id, scope: 'organization' as const, scope_id: id, standard_id: id,
    admission_version: 2, expected_revision: 1, active: false, batch_size: 10 };
  const review = { id, org_id: id, created_by: id, request: body, approval_hash: 'a'.repeat(64),
    applications: [], blockers: [], created_at: time, expires_at: time };
  const server = createServer((req, res) => {
    void (async () => {
      let raw = ''; for await (const chunk of req) raw += String(chunk);
      requests.push({ method: req.method, path: req.url, body: raw ? JSON.parse(raw) : undefined, auth: req.headers.authorization });
      res.setHeader('Content-Type', 'application/json');
      if (req.url?.includes('/exceptions')) {
        res.end(JSON.stringify({ exceptions: [{ id, org_id: id, app_id: id, standard_id: id, version: 1,
          field: 'egress_extra_ports', value: [5432], reason: 'Maintenance', expires_at: time, approved_by: id,
          created_at: time, revoked_by: id, revoked_at: time, status: 'revoked' }], as_of: time }));
      } else if (req.url?.includes('/application-standard-operations/')) {
        res.end(JSON.stringify({ id, org_id: id, plan_id: id, assignment_id: id, approval_hash: 'a'.repeat(64),
          approved_by: id, batch_size: 10, state: 'waiting', targets: [], created_at: time, updated_at: time }));
      } else if (req.url?.includes('/application-standard-enrollments/')) {
        res.end(JSON.stringify({ app_id: id, org_id: id, local_settings: {}, additional_log_destinations: [],
          adoptions: [{ assignment_id: id, version: 2 }], materialized_fields: [], desired_revision: 2,
          persisted_revision: 2, observed_revision: 0, state: 'persisted', updated_at: time,
          installed_exception_expires_at: time }));
      } else {
        res.statusCode = req.method === 'POST' ? 201 : 200;
        res.end(JSON.stringify(review));
      }
    })().catch(() => { res.statusCode = 500; res.end('{}'); });
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  const client = new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'fixture' });
  t.after(() => client.uninstall());
  const preview = await OrgsService.previewApplicationStandardAssignment({ slug: 'acme', requestBody: body });
  assert.equal(preview.request.active, false); assert.equal(preview.request.expected_revision, 1);
  assert.equal((await OrgsService.getApplicationStandardReview({ slug: 'acme', review: id })).id, id);
  assert.equal((await OrgsService.getApplicationStandardOperation({ slug: 'acme', operation: id })).state, 'waiting');
  const exceptions = await OrgsService.listApplicationStandardExceptions({ slug: 'acme', app: id, after: id, limit: 1 });
  assert.equal(exceptions.exceptions[0]?.status, 'revoked'); assert.deepEqual(exceptions.exceptions[0]?.value, [5432]);
  const enrollment = await OrgsService.getApplicationStandardEnrollment({ slug: 'acme', app: id });
  assert.equal(enrollment.installed_exception_expires_at, time); assert.equal(enrollment.observed_revision, 0);
  assert.deepEqual(requests.map(({ method, path, body }) => ({ method, path, body })), [
    { method: 'POST', path: '/v1/orgs/acme/application-standard-reviews', body },
    { method: 'GET', path: `/v1/orgs/acme/application-standard-reviews/${id}`, body: undefined },
    { method: 'GET', path: `/v1/orgs/acme/application-standard-operations/${id}`, body: undefined },
    { method: 'GET', path: `/v1/orgs/acme/application-standard-enrollments/${id}/exceptions?after=${id}&limit=1`, body: undefined },
    { method: 'GET', path: `/v1/orgs/acme/application-standard-enrollments/${id}`, body: undefined },
  ]);
  assert.ok(requests.every(request => request.auth === 'Bearer fixture'));
});
