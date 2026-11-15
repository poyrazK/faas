import pg from 'pg';
const pool = new pg.Pool({ connectionString: process.env.DATABASE_URL, connectionTimeoutMillis: 3000 });
let stopping = false;
process.on('SIGTERM', () => { stopping = true; });
while (!stopping) {
  try {
    await pool.query('CREATE TABLE IF NOT EXISTS gregale_sdk_example (id bigserial PRIMARY KEY, created_at timestamptz NOT NULL DEFAULT now())');
    await pool.query('INSERT INTO gregale_sdk_example DEFAULT VALUES');
  } catch { /* The managed database can still be provisioning. Retry without logging credentials. */ }
  await new Promise(resolve => setTimeout(resolve, 2000));
}
await pool.end();
