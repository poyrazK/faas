import { createServer } from 'node:http';
import { pathToFileURL } from 'node:url';
import { createDevBridgeFetch, withDevBridgeRequestContext, withDevBridgeContext } from '../../../sdk/node/dist/dev-bridge.js';

// An isolated acceptance fixture, never a customer template. The custom probe
// captures authority inside a production VM to exercise service-hop denial.
export function fixtureServer({ role, payments = 'http://payments.svc.gregale:10080', inventory = 'http://inventory.svc.gregale:10080', fetchImpl = globalThis.fetch }) {
  const fetch = createDevBridgeFetch(fetchImpl);
  return createServer((request, response) => {
    const reply = (status, value) => {
      response.writeHead(status, { 'Content-Type': 'application/json' });
      response.end(JSON.stringify(value));
    };
    if (request.method === 'GET' && ['/', '/health', '/healthz'].includes(request.url)) return reply(200, { ready: true });
    const handle = async () => {
      if (role === 'inventory' && request.url === '/stock') return reply(200, { inventory: 'remote' });
      if (!['frontend', 'payments'].includes(role) || request.url !== '/charge') return reply(404, { error: 'unknown fixture route' });
      const endpoint = role === 'frontend' ? payments + '/charge' : inventory + '/stock';
      const headers = request.headers.authorization ? { Authorization: request.headers.authorization } : {};
      try {
        const upstream = await fetch(endpoint, { headers, signal: AbortSignal.timeout(15000) });
        if (upstream.status !== 200) return reply(upstream.status, { caller: role, upstream_status: upstream.status });
        const value = await upstream.json();
        return reply(200, role === 'payments' ? { payments: 'remote', inventory: value.inventory } : value);
      } catch {
        return reply(502, { caller: role, error: 'fixture service hop failed' });
      }
    };
    const probe = request.headers['x-gregale-bridge-acceptance-context'];
    if (role === 'frontend' && typeof probe === 'string') {
      void withDevBridgeContext(probe, handle);
    } else {
      void withDevBridgeRequestContext(request.headers, handle);
    }
  });
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const role = process.argv[2] || process.env.BRIDGE_FIXTURE_ROLE;
  if (!['frontend', 'payments', 'inventory'].includes(role)) throw new Error('Select BRIDGE_FIXTURE_ROLE');
  const server = fixtureServer({ role, payments: process.env.GREGALE_SERVICE_PAYMENTS_URL, inventory: process.env.GREGALE_SERVICE_INVENTORY_URL });
  server.listen(Number(process.env.PORT || 8080), '0.0.0.0');
  for (const signal of ['SIGTERM', 'SIGINT']) process.on(signal, () => server.close(() => process.exit(0)));
}
