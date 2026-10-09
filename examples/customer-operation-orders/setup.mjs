import {readFile} from 'node:fs/promises';
import pg from 'pg';
import {customerOperationReceiptSchema} from './sdk.mjs';

if (!process.env.APPLICATION_DATABASE_URL) throw new Error('Set APPLICATION_DATABASE_URL');
const pool = new pg.Pool({connectionString: process.env.APPLICATION_DATABASE_URL});
try {
  await pool.query(await readFile(new URL('./schema.sql', import.meta.url), 'utf8'));
  await pool.query(customerOperationReceiptSchema);
} finally { await pool.end(); }
