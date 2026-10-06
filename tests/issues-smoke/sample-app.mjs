import { createServer } from 'node:http';
import { createIssueReporter } from '../../sdk/node/dist/index.js';

function smokeError(kind) {
  const error = new Error(`Controlled Gregale Issues ${kind.toLowerCase()} smoke failure`);
  error.name = `GregaleIssuesSmoke${kind}Failure`;
  // Keep the example frame stable and avoid sending a workstation path to staging.
  error.stack = `${error.name}: ${error.message}\n    at generateExport (/build/issues-smoke/export.js:42:1)`;
  return error;
}

function respond(response, status, body) {
  response.writeHead(status, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' });
  response.end(JSON.stringify(body));
}

/** Starts a tiny HTTP app whose handlers report representative request and
 * worker failures through Gregale's Node SDK. */
export async function startIssuesSmokeSample({ baseURL, app, token, fetch: fetchImpl = globalThis.fetch }) {
  const reporter = createIssueReporter({ baseURL, app, token, fetch: fetchImpl, maxQueue: 2, timeoutMs: 8000 });
  const server = createServer(async (request, response) => {
    try {
      const pathname = new URL(request.url || '/', 'http://127.0.0.1').pathname;
      const scenario = pathname === '/fail/http' ? 'HTTP' : pathname === '/fail/worker' ? 'Worker' : '';
      if (request.method !== 'POST' || !scenario) {
        respond(response, 404, { error: 'not found' });
        return;
      }

      const context = {
        route: scenario === 'HTTP' ? 'POST /fail/http' : 'gregale-issues-smoke:worker',
        source_kind: scenario === 'HTTP' ? 'exception' : 'worker',
      };
      if (scenario === 'HTTP' && typeof request.headers['x-gregale-request-id'] === 'string') {
        context.request_id = request.headers['x-gregale-request-id'];
      }
      if (scenario === 'Worker' && typeof request.headers['x-gregale-invocation-id'] === 'string') {
        context.invocation_id = request.headers['x-gregale-invocation-id'];
      }

      const eventID = reporter.captureException(smokeError(scenario), context);
      if (!eventID || !await reporter.flush()) {
        respond(response, 503, { error: 'issue event was not delivered' });
        return;
      }
      // The request fails as expected; the returned event ID lets the smoke
      // driver verify the corresponding Gregale issue and release history.
      respond(response, 500, { event_id: eventID, exception_type: `GregaleIssuesSmoke${scenario}Failure` });
    } catch {
      if (!response.headersSent) respond(response, 503, { error: 'smoke sample failed to report an issue event' });
    }
  });

  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      server.off('error', reject);
      resolve();
    });
  });
  const address = server.address();
  if (!address || typeof address === 'string') throw new Error('smoke sample did not bind a TCP port');

  return {
    url: `http://127.0.0.1:${address.port}`,
    stats: reporter.stats,
    async close() {
      await new Promise((resolve, reject) => server.close(error => error ? reject(error) : resolve()));
      return reporter.close(10000);
    },
  };
}
