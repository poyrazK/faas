// Called by the isolated metal harness. Output only the customer projection.
import { readFileSync, writeSync } from 'node:fs';
import { createRequire } from 'node:module';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

const [bundle, requestID, customerID, operation] = process.argv.slice(2);
const require = createRequire(path.join(bundle, 'package.json'));
const { Client } = require('pg');
const { createReportIntake } = await import(pathToFileURL(path.join(bundle, 'report-intake.ts')));
const db = new Client({ connectionString: process.env.DATABASE_URL });
await db.connect();
try {
  const lostAcknowledgement = new Error('fixture dropped the admitted rerun response');
  const completionWebhookSecret = process.env.REPORT_COMPLETION_WEBHOOK_SECRET;
  const crash = operation === 'completion-crash';
  const deliveryPath = '/v1/apps/report-agent/run-artifact-deliveries/reports';
  const reports = createReportIntake({
    db, apiURL: process.env.GREGALE_API_URL, token: process.env.GREGALE_TEST_TOKEN,
    app: 'report-agent', endpointID: process.env.REPORT_ENDPOINT_ID, uploadRoute: 'reports',
    taskPrefix: process.env.REPORT_TASK_PREFIX,
    completionWebhookSecret,
    ...(operation === 'rerun-drop-ack' || crash ? { fetch: async (url, options) => {
      const response = await fetch(url, options);
      if (operation === 'rerun-drop-ack' && options.method === 'POST' && new URL(url).pathname.endsWith('/reruns') && response.status === 202) {
        await response.body?.cancel();
        throw lostAcknowledgement;
      }
      if (crash && options.method === 'POST' && new URL(url).pathname === deliveryPath && response.ok) {
        const { id, status } = await response.clone().json();
        if (status === 'completed') {
          writeSync(1, JSON.stringify({ pid: process.pid, artifactID: id }) + '\n');
          process.exit(86);
        }
      }
      return response;
    } } : {}),
  });
  const key = { requestID, customerID };
  const input = () => JSON.parse(readFileSync(0, 'utf8'));
  let result;
  if (operation === 'completion' || crash) {
    const delivery = input();
    const headers = new Headers();
    for (const [name, values] of Object.entries(delivery.headers)) {
      for (const value of values) headers.append(name, value);
    }
    const response = await reports.receiveCompletion(new Request('http://localhost/report-completion', {
      method: 'POST', headers, body: Buffer.from(delivery.body, 'base64'),
    }));
    result = { pid: process.pid, status: response.status };
  } else if (operation === 'submit') result = await reports.submit(key, new Blob([readFileSync(0)]));
  else if (operation === 'submit-regional') result = await reports.submitRegional(key, input());
  else if (operation === 'progress') result = await reports.progress(key, input());
  else if (operation === 'rerun' || operation === 'rerun-drop-ack') {
    try { result = await reports.rerun(key, input()); }
    catch (error) {
      if (error !== lostAcknowledgement) throw error;
      result = { droppedAcceptedResponse: true };
    }
  } else if (operation === 'status' || operation === 'download') result = await reports[operation](key);
  else throw new Error('Unknown Report Agent test operation');
  console.log(JSON.stringify(result));
} finally {
  await db.end();
}
