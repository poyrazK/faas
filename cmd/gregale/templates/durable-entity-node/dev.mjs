// This file is excluded from source deployments. No account or bucket credentials are used.
import {createServer} from 'node:http';
import {fileURLToPath} from 'node:url';
import {DurableEntitySimulator} from '@gregale/sdk-node/durable-entities/testing';
import {registry} from './registry.mjs';
import {readBody} from './server.mjs';

export function createLocalServer() {
  const simulator = new DurableEntitySimulator(registry);
  const server = createServer(async (request, response) => {
    response.setHeader('Cache-Control', 'no-store');
    response.setHeader('Content-Type', 'application/json');
    const url = new URL(request.url, 'http://localhost');
    try {
      let result;
      if (request.method === 'POST' && url.pathname === '/invoke') {
        const input = JSON.parse((await readBody(request)).toString());
        result = await simulator.invoke({namespace: input.namespace, key: input.key, requestId: input.request_id, payload: input.payload}, {fault: input.fault});
      } else if (request.method === 'GET' && url.pathname === '/inspect') {
        result = simulator.inspect(url.searchParams.get('namespace'), url.searchParams.get('key')) ?? null;
      } else if (request.method === 'POST' && url.pathname === '/advance') {
        const input = JSON.parse((await readBody(request)).toString());
        simulator.advance(input.milliseconds);
        result = {now: simulator.now().toISOString(), alarms: await simulator.runDueAlarms()};
      } else if (request.method === 'GET' && url.pathname === '/checkpoint') {
        response.end(simulator.checkpoint()); return;
      } else if (request.method === 'POST' && url.pathname === '/checkpoint') {
        simulator.restore((await readBody(request, 16 << 20)).toString()); result = {restored: true};
      } else { response.writeHead(404).end(JSON.stringify({error: 'unknown local route'})); return; }
      response.end(JSON.stringify(result));
    } catch (error) {
      response.writeHead(error instanceof RangeError ? 413 : 409).end(JSON.stringify({error: 'local invocation failed', phase: error?.phase}));
    }
  });
  return {server, simulator};
}
if (process.argv[1] === fileURLToPath(import.meta.url)) {
  createLocalServer().server.listen(Number(process.env.PORT ?? 8787), '127.0.0.1', () => {
    process.stdout.write('Local entity simulator: http://127.0.0.1:8787 (virtual clock starts at 2000-01-01T00:00:00Z)\n');
  });
}
