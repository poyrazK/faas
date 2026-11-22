// Loopback-only adapter for the browser acceptance fixture.
import { once } from 'node:events';
import { createServer } from 'node:http';
import { createRequire } from 'node:module';
import path from 'node:path';
import { Readable } from 'node:stream';
import { pathToFileURL } from 'node:url';

const [bundle, rawPort] = process.argv.slice(2);
const port = Number(rawPort);
if (!Number.isInteger(port) || port < 1024 || port > 65535) throw new Error('Invalid browser acceptance port');
const origin = `http://127.0.0.1:${port}`;
const require = createRequire(path.join(bundle, 'package.json'));
const { Pool } = require('pg');
const { createReportIntake } = await import(pathToFileURL(path.join(bundle, 'report-intake.ts')));
const { createReportPage } = await import(pathToFileURL(path.join(bundle, 'report-page.mjs')));
const db = new Pool({ connectionString: process.env.DATABASE_URL });
let pageClosed = false, deliveryReady = false, browserPassed = false;
let rerunPosts = 0, heldCompletions = 0, closedPolls = 0;
let admittedWorkflow;
const reports = createReportIntake({
  db, app: 'report-agent', uploadRoute: 'reports', apiURL: process.env.GREGALE_API_URL,
  token: process.env.GREGALE_TEST_TOKEN, endpointID: process.env.REPORT_ENDPOINT_ID,
  completionWebhookSecret: process.env.REPORT_COMPLETION_WEBHOOK_SECRET,
  async fetch(url, options) {
    const response = await fetch(url, options);
    if (options.method === 'POST' && new URL(url).pathname.endsWith('/reruns')) {
      const intent = JSON.parse(options.body);
      if (rerunPosts && admittedWorkflow !== intent.workflow_id) throw new Error('Rerun changed workflow identity');
      if (++rerunPosts === 1 && response.status === 202) {
        admittedWorkflow = intent.workflow_id;
        await response.body?.cancel();
        throw new Error('Fixture dropped the accepted rerun response');
      }
    }
    return response;
  },
});

const customerForRequest = request => request.headers.get('cookie')?.match(/(?:^|; )report-fixture=(owner|other)(?:;|$)/)?.[1];
const page = createReportPage({ reports, customerForRequest, publicOrigin: origin });
const fixtureStatus = () => ({ pageClosed, deliveryReady, rerunPosts, heldCompletions, closedPolls, browserPassed });

async function handle(request) {
  const url = new URL(request.url);
  if (url.pathname === '/__fixture/health') return new Response(null, { status: 204 });
  if (url.pathname === '/report-completion') {
    // Preserve the failed receipt immediately. Hold only the successful
    // export until Chromium has closed its page.
    const event = await request.clone().json();
    if (event?.data?.outcome === 'succeeded' && !pageClosed) {
      heldCompletions++;
      return new Response(null, { status: 503 });
    }
    const response = await reports.receiveCompletion(request);
    if (response.status === 204 && event?.data?.outcome === 'succeeded') deliveryReady = true;
    return response;
  }
  if (url.pathname.startsWith('/__fixture/')) {
    if (customerForRequest(request) !== 'owner') return new Response(null, { status: 404 });
    if (url.pathname === '/__fixture/status' && request.method === 'GET') return Response.json(fixtureStatus());
    if (request.method !== 'POST') return new Response(null, { status: 405 });
    if (url.pathname === '/__fixture/page-closed') pageClosed = true;
    else if (url.pathname === '/__fixture/browser-passed') browserPassed = true;
    else return new Response(null, { status: 404 });
    return new Response(null, { status: 204 });
  }
  if (pageClosed && !deliveryReady && url.pathname.endsWith('/progress')) closedPolls++;
  return await page(request) ?? new Response(null, { status: 404 });
}

const server = createServer(async (incoming, outgoing) => {
  try {
    const { method, headers, url } = incoming;
    const init = { method, headers };
    if (method === 'POST') Object.assign(init, { body: Readable.toWeb(incoming), duplex: 'half' });
    const response = await handle(new Request(origin + url, init));
    const body = Buffer.from(await response.arrayBuffer());
    outgoing.writeHead(response.status, { ...Object.fromEntries(response.headers), 'Content-Length': body.length }).end(body);
  } catch {
    outgoing.writeHead(503).end();
  }
});
server.listen(port, '127.0.0.1');
await once(server, 'listening');
