import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { FaaSClient, QueuesService, ProjectsService } from '../src/index.js';

test('organization queue clients preserve queue and project ownership paths', async (t) => {
  const requests: string[] = [];
  const server = createServer((req, res) => {
    requests.push(`${req.method} ${req.url}`);
    assert.equal(req.headers.authorization, 'Bearer company-token');
    res.setHeader('Content-Type', 'application/json');
    res.end(JSON.stringify({ messages: [] }));
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address();
  assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'company-token', retry: { maxAttempts: 1, backoffMs: 0 } });
  await QueuesService.leaseOrgQueueMessages({ organization: 'my company', slug: 'app/id', queue: 'jobs/id' });
  await QueuesService.ackOrgQueueLeases({ organization: 'my company', slug: 'app/id', queue: 'jobs/id', requestBody: { receipts: ['receipt'] } });
  await ProjectsService.getOrgProjectEnvironmentQueueBindings({ organization: 'my company', slug: 'project/id', environment: 'stage', workload: 'worker/id' });
  await QueuesService.getOrgQueueUsage({ organization: 'my company' });
  assert.deepEqual(requests, [
    'POST /v1/orgs/my%20company/apps/app%2Fid/queues/jobs%2Fid/leases',
    'POST /v1/orgs/my%20company/apps/app%2Fid/queues/jobs%2Fid/leases:ack',
    'GET /v1/orgs/my%20company/projects/project%2Fid/environments/stage/workloads/worker%2Fid/queue-bindings',
    'GET /v1/orgs/my%20company/queue-usage',
  ]);
});
