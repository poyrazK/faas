import { Pool } from 'pg';
import { createPostgresMcpTaskStore } from '../task-store.js';
import { createMcpTaskRuntime } from '../tasks.js';

const {
  MCP_TASKS_TEST_DATABASE_URL: databaseURL,
  MCP_TASKS_TEST_NAMESPACE: namespace,
  MCP_TASKS_TEST_OWNER_KEY: ownerKey,
  MCP_TASKS_TEST_SCHEMA: schema,
  MCP_TASKS_TEST_WORKER_ID: workerID,
} = process.env;

if (!databaseURL || !namespace || !ownerKey || !schema || !workerID || !/^[a-z0-9_]+$/.test(schema)) {
  process.send?.({ type: 'fatal', reason: 'worker_configuration_invalid' });
  process.disconnect?.();
  process.exitCode = 1;
} else {
  const pool = new Pool({
    connectionString: databaseURL,
    max: 2,
    connectionTimeoutMillis: 5_000,
    statement_timeout: 10_000,
    options: `-c search_path=${schema}`,
  });
  pool.on('error', () => {
    process.send?.({ type: 'fatal', reason: 'worker_database_connection_failed' });
  });
  const store = createPostgresMcpTaskStore({ pool, namespace, ownerKey, ttlMs: 60_000 });
  const runtime = createMcpTaskRuntime({
    store,
    pollIntervalMs: 500,
    keepAlive: true,
    handlers: {
      build_report: {
        version: '1',
        async execute({ qualificationRunID }, { taskId }) {
          await pool.query(
            'INSERT INTO gregale_mcp_qualification_runs (run_id, worker_id) VALUES ($1::uuid, $2)',
            [qualificationRunID, workerID],
          );
          process.send?.({ type: 'started', taskId, workerID });
          await new Promise(resolve => {
            const onMessage = message => {
              if (message?.type !== 'release') return;
              process.off('message', onMessage);
              resolve();
            };
            process.on('message', onMessage);
          });
          return { content: [{ type: 'text', text: 'qualification complete' }] };
        },
      },
    },
  });

  let closing;
  async function close() {
    if (closing) return closing;
    closing = (async () => {
      await runtime.stop();
      await pool.end();
    })();
    return closing;
  }

  process.once('SIGTERM', () => {
    void close().then(() => {
      process.send?.({ type: 'stopped' });
      process.disconnect?.();
    }).catch(() => {
      process.exitCode = 1;
      process.disconnect?.();
    });
  });
  process.once('SIGINT', () => {
    void close().catch(() => { process.exitCode = 1; });
  });

  try {
    await runtime.start();
    process.send?.({ type: 'ready', workerID });
  } catch {
    process.send?.({ type: 'fatal', reason: 'worker_start_failed' });
    await pool.end().catch(() => {});
    process.exitCode = 1;
  }
}
