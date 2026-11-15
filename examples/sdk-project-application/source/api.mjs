import http from 'node:http';
import pg from 'pg';
const pool = new pg.Pool({ connectionString: process.env.DATABASE_URL, connectionTimeoutMillis: 3000 });
const server = http.createServer(async (_request, response) => {
  try {
    const result = await pool.query('SELECT count(*)::int AS processed FROM gregale_sdk_example');
    response.writeHead(200, { 'Content-Type': 'application/json' });
    response.end(JSON.stringify({ ok: true, processed: result.rows[0].processed }));
  } catch {
    response.writeHead(503); response.end('Waiting for the database and worker');
  }
});
server.listen(Number(process.env.PORT ?? 8080), '0.0.0.0');
process.on('SIGTERM', () => server.close(() => { void pool.end(); }));
