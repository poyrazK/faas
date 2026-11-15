import http from 'node:http';
import pg from 'pg';
const pool = new pg.Pool({ connectionString: process.env.DATABASE_URL, connectionTimeoutMillis: 3000 });
const server = http.createServer(async (request, response) => {
  try {
    const result = await pool.query('SELECT count(*)::int AS processed FROM gregale_sdk_example');
    const expected = new URL(request.url, 'http://localhost').searchParams.get('worker_deployment_id') ?? '';
    const worker = await pool.query('SELECT deployment_id, status, observed_at FROM gregale_sdk_worker_status WHERE deployment_id = $1', [expected]);
    response.writeHead(200, { 'Content-Type': 'application/json' });
    response.end(JSON.stringify({ ok: true, deployment_id: process.env.FAAS_DEPLOYMENT_ID ?? null, processed: result.rows[0].processed, worker: worker.rows[0] ?? null }));
  } catch {
    response.writeHead(503); response.end('Waiting for the database and worker');
  }
});
server.listen(Number(process.env.PORT ?? 8080), '0.0.0.0');
process.on('SIGTERM', () => server.close(() => { void pool.end(); }));
