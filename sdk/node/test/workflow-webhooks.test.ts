import assert from 'node:assert/strict';
import { once } from 'node:events';
import { createServer } from 'node:http';
import test from 'node:test';
import { FaaSClient, InboundWebhooksService } from '../src/index.js';

test('webhook automation bindings preserve revision zero, takeover and receipt routes', async t => {
  let seen = 0;
  const server = createServer((req, res) => {
    void (async () => {
      seen++;
      assert.equal(req.headers.authorization, 'Bearer token');
      res.setHeader('content-type', 'application/json');
      if (req.method === 'DELETE') {
        assert.ok(req.url?.endsWith('/automation-binding?expected_version=7'));
        res.statusCode = 204; res.end(); return;
      }
      if (req.url?.endsWith('/automation-receipts/evt_1')) {
        res.end(JSON.stringify({ receipt_id: 'receipt', endpoint_id: 'endpoint', provider_event_id: 'evt_1', workflow_name: 'paid', status: 'accepted', duplicate: false, accepted_at: '2026-10-03T12:00:00Z', event_source: 'gregale.inbound.stripe.endpoint', routing_status: 'enqueued', run_id: 'run' })); return;
      }
      assert.equal(req.url, '/v1/apps/billing/inbound-webhooks/endpoint/automation-binding');
      if (req.method === 'PUT') {
        const chunks: Buffer[] = [];
        for await (const chunk of req) chunks.push(Buffer.from(chunk));
        assert.deepEqual(JSON.parse(Buffer.concat(chunks).toString()), { expected_version: 0, workflow_name: 'paid', event_type: 'invoice.*', take_over_delivery: true });
        assert.ok(req.headers['idempotency-key']);
      }
      res.end(JSON.stringify({ endpoint_id: 'endpoint', workflow_name: 'paid', event_type: 'invoice.*', filter: {}, version: 7, updated_at: '2026-10-03T12:00:00Z' }));
    })().catch(error => { res.statusCode = 500; res.end(JSON.stringify({ error: String(error) })); });
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'token', retry: { maxAttempts: 1, backoffMs: 0 } });
  const binding = await InboundWebhooksService.putWebhookAutomationBinding({ slug: 'billing', id: 'endpoint', requestBody: { expected_version: 0, workflow_name: 'paid', event_type: 'invoice.*', take_over_delivery: true } });
  assert.equal(binding.version, 7);
  assert.equal((await InboundWebhooksService.getWebhookAutomationBinding({ slug: 'billing', id: 'endpoint' })).workflow_name, 'paid');
  assert.equal((await InboundWebhooksService.getWebhookAutomationReceipt({ slug: 'billing', id: 'endpoint', eventId: 'evt_1' })).run_id, 'run');
  await InboundWebhooksService.deleteWebhookAutomationBinding({ slug: 'billing', id: 'endpoint', expectedVersion: 7 });
  assert.equal(seen, 4);
});
