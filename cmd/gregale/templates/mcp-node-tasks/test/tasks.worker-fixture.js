import { Pool } from 'pg';
import { createPostgresMcpTaskStore } from '../task-store.js';
import { createMcpTaskRuntime } from '../tasks.js';

const { MCP_TASKS_TEST_DATABASE_URL: databaseURL, MCP_TASKS_TEST_NAMESPACE: namespace,
  MCP_TASKS_TEST_OWNER_KEY: ownerKey, MCP_TASKS_TEST_SCHEMA: schema } = process.env;

if (!databaseURL || !namespace || !ownerKey || !schema || !/^[a-z0-9_]+$/.test(schema)) {
  throw new Error('PostgreSQL worker fixture configuration is incomplete');
}

const pool = new Pool({
  connectionString: databaseURL,
  max: 2,
  connectionTimeoutMillis: 5_000,
  statement_timeout: 10_000,
  options: `-c search_path=${schema}`,
});
const store = createPostgresMcpTaskStore({ pool, namespace, ownerKey, ttlMs: 60_000 });
const runtime = createMcpTaskRuntime({
  store,
  pollIntervalMs: 500,
  handlers: {
    build_report: {
      version: '1',
      async execute(_args, { taskId }) {
        process.send({ type: 'started', taskId });
        return new Promise(() => {});
      },
    },
  },
});

try {
  await runtime.start();
  process.send({ type: 'ready' });
} catch (error) {
  process.send({ type: 'fatal', message: error instanceof Error ? error.message : 'worker startup failed' });
  await pool.end();
  process.exitCode = 1;
}
