import {createServer} from 'node:http';
import pg from 'pg';
import {SecretReloadCoordinator} from '@gregale/sdk-node/secrets/runtime';
import {createPostgresBinding, withPostgresTransaction} from './pool.mjs';

const binding = createPostgresBinding(pg.Pool);
const coordinator = new SecretReloadCoordinator({consumers: [binding.secretConsumer()],
  onError: () => console.error('Secret adoption failed')});

// The coordinator installs SIGHUP before readiness and loads Gregale's atomic
// snapshot. It ACKs only after the replacement pool passes SELECT 1.
try {
  await coordinator.start();
} catch {
  console.error('Initial secret adoption failed');
  coordinator.stop();
  await binding.waitForRetirement(AbortSignal.timeout(5000)).catch(() => {process.exitCode = 1;});
  process.exitCode = 1;
}

if (!process.exitCode) {
  const server = createServer((request, response) => {
    void (async () => {
      if (request.method !== 'GET' || request.url !== '/db-time') {response.writeHead(404).end(); return;}
      const time = await withPostgresTransaction(binding, async connection => {
        const {rows} = await connection.query('SELECT CURRENT_TIMESTAMP AS time');
        return rows[0].time;
      });
      response.writeHead(200, {'content-type': 'application/json'}).end(JSON.stringify({time}));
    })().catch(() => response.writeHead(503).end('Database unavailable'));
  });

  server.listen(Number(process.env.PORT ?? 8080), '0.0.0.0');
  let stopping = false;
  const shutdown = () => {
    if (stopping) return;
    stopping = true;
    server.close();
    coordinator.stop(); // Fences acquisitions; leased transactions keep running.
    void binding.waitForRetirement(AbortSignal.timeout(5000)).catch(() => {
      console.error('Postgres pool drain incomplete');
      process.exitCode = 1;
    });
  };
  process.once('SIGTERM', shutdown);
  process.once('SIGINT', shutdown);
}
