import { createServer } from 'node:http';
import { fileURLToPath } from 'node:url';
import { Pool } from 'pg';
import { issueSlackMessageFromWebhook, formatIssueSlackMessage, WebhookVerificationError } from './sdk.mjs';
import { PostgresReceipts, ReceiptConflict } from './receipts.mjs';

// Receiver cap matches Gregale's existing 1 MiB webhook body limit.
const MAX_BODY_BYTES = 1 << 20;

function validateSlackURL(raw) {
  let url;
  try { url = new URL(raw); } catch { throw new TypeError('invalid Slack webhook configuration'); }
  if (url.protocol !== 'https:' || url.hostname !== 'hooks.slack.com' || url.port || url.username || url.password
    || url.search || url.hash || !/^\/services\/[^/]+\/[^/]+\/[^/]+$/.test(url.pathname)) throw new TypeError('invalid Slack webhook configuration');
  return url.href;
}
export function createIssueSlackServer({ secret, slackURL, options, receipts, fetch: send = globalThis.fetch }) {
  if (typeof secret !== 'string' || !secret || !receipts) throw new TypeError('missing bridge configuration');
  const destination = validateSlackURL(slackURL);
  // Validate routing configuration at startup without producing an alert.
  formatIssueSlackMessage({ type: 'ignored' }, options);
  return createServer(async (req, res) => {
    res.setHeader('Cache-Control', 'no-store');
    if (req.method === 'GET' && req.url === '/healthz') { res.end('ok'); return; }
    if (req.method !== 'POST' || req.url !== '/webhooks/gregale') { res.writeHead(404).end(); return; }
    let deliveryID, token;
    try {
      const chunks = []; let bytes = 0;
      for await (const chunk of req) {
        bytes += chunk.length;
        if (bytes > MAX_BODY_BYTES) { res.writeHead(413).end('webhook body too large'); return; }
        chunks.push(chunk);
      }
      const body = Buffer.concat(chunks);
      const verified = issueSlackMessageFromWebhook(secret, req.headers, body, options);
      if (!verified.message) { res.writeHead(204).end(); return; }
      deliveryID = verified.deliveryId;
      const claim = await receipts.claim(deliveryID, verified.transitionSHA256);
      if (claim.status === 'sent') { res.writeHead(204).end(); return; }
      if (claim.status === 'busy') { res.writeHead(503, { 'Retry-After': '1' }).end('delivery in progress'); return; }
      token = claim.token;
      const response = await send(destination, { method: 'POST', redirect: 'error', signal: AbortSignal.timeout(5000),
        headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(verified.message) });
      await response.body?.cancel();
      if (response.status !== 200) throw new Error('Slack did not accept delivery');
      await receipts.complete(deliveryID, token);
      res.writeHead(204).end();
    } catch (error) {
      if (deliveryID && token) { try { await receipts.release(deliveryID, token); } catch {} }
      // Never echo signing secrets, provider URLs, payloads or provider response bodies.
      const status = error instanceof WebhookVerificationError ? 401 : error instanceof ReceiptConflict ? 409 : error instanceof TypeError && !token ? 422 : 503;
      res.writeHead(status).end(status === 503 ? 'delivery unavailable; retry later' : 'invalid webhook delivery');
    }
  });
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  if (!process.env.DATABASE_URL) throw new TypeError('DATABASE_URL is required');
  const pool = new Pool({ connectionString: process.env.DATABASE_URL, connectionTimeoutMillis: 5000, query_timeout: 5000, max: 4 });
  const server = createIssueSlackServer({ secret: process.env.GREGALE_WEBHOOK_SECRET, slackURL: process.env.SLACK_WEBHOOK_URL,
    options: { appID: process.env.ISSUE_APP_ID, appSlug: process.env.ISSUE_APP_SLUG, dashboardOrigin: process.env.GREGALE_DASHBOARD_ORIGIN,
      teamID: process.env.ISSUE_TEAM_ID || undefined,
      events: process.env.ISSUE_EVENTS ? process.env.ISSUE_EVENTS.split(',') : undefined }, receipts: new PostgresReceipts(pool) });
  server.listen(Number(process.env.PORT || 8080), '0.0.0.0');
}
