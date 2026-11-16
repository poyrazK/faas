import pg from 'pg';
import { readFile } from 'node:fs/promises';

// Source-controlled fault injection for the verification example; deploy a new
// reviewed plan after changing this file. No runtime credentials are recorded.
const fault = JSON.parse(await readFile(new URL('worker-fault.json', import.meta.url), 'utf8')).fail === true;
const deploymentId = process.env.FAAS_DEPLOYMENT_ID ?? 'local-worker';
const pool = new pg.Pool({ connectionString: process.env.DATABASE_URL, connectionTimeoutMillis: 3000 });
let stopping = false;
process.on('SIGTERM', () => { stopping = true; });
while (!stopping) {
  try {
    await pool.query('CREATE TABLE IF NOT EXISTS gregale_sdk_example (id bigserial PRIMARY KEY, created_at timestamptz NOT NULL DEFAULT now())');
    await pool.query('CREATE TABLE IF NOT EXISTS gregale_sdk_worker_status (deployment_id text PRIMARY KEY, status text NOT NULL, observed_at timestamptz NOT NULL)');
    if (!fault) await pool.query('INSERT INTO gregale_sdk_example DEFAULT VALUES');
    await pool.query("INSERT INTO gregale_sdk_worker_status VALUES ($1, $2, now()) ON CONFLICT (deployment_id) DO UPDATE SET status = excluded.status, observed_at = excluded.observed_at", [deploymentId, fault ? 'failed' : 'processed']);
  } catch { /* The managed database can still be provisioning. Retry without logging credentials. */ }
  await new Promise(resolve => setTimeout(resolve, 2000));
}
await pool.end();
