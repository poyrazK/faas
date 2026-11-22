import { readFileSync } from 'node:fs';
import pg from 'pg';
import { resolveMcpTaskSettings } from './task-runtime.js';
import { mcpTaskHandlers } from './tasks.js';
import { checkMcpTaskCompatibility } from './task-compatibility.js';

let pool;
try {
  const config = JSON.parse(readFileSync(new URL('./gregale-mcp.json', import.meta.url)));
  // Only database and namespace bindings are needed, as with a read-only observer.
  const settings = resolveMcpTaskSettings(config, { role: 'observer' });
  pool = new pg.Pool({ connectionString: settings.databaseURL, max: 1, connectionTimeoutMillis: 5000, statement_timeout: 10000 });
  const report = await checkMcpTaskCompatibility({ pool, namespace: settings.namespace, handlers: mcpTaskHandlers });
  console.log(JSON.stringify(report));
  process.exitCode = report.ok ? 0 : 1;
} catch {
  console.log(JSON.stringify({ ok: false, checks: [{ name: 'retained_handler_coverage', status: 'unknown', detail: 'Check candidate handler registry, Task database schema and namespace binding' }] }));
  process.exitCode = 1;
} finally {
  await pool?.end().catch(() => {});
}
