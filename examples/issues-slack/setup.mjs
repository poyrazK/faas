import { readFile } from 'node:fs/promises';
import { Pool } from 'pg';
if (!process.env.DATABASE_URL) throw new TypeError('DATABASE_URL is required');
const pool = new Pool({ connectionString: process.env.DATABASE_URL, connectionTimeoutMillis: 5000 });
try { await pool.query(await readFile(new URL('schema.sql', import.meta.url), 'utf8')); }
finally { await pool.end(); }
