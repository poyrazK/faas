import { readFileSync } from 'node:fs';
import pg from 'pg';
import { resolveMcpTaskSettings } from './task-runtime.js';
import { mcpTaskHandlers } from './tasks.js';
import { checkMcpTaskCompatibility, mcpTaskHandlerInventory } from './task-compatibility.js';
import { createMcpTaskAdmissionController } from './task-admission.js';
import { checkMcpTaskReplacementReadiness } from './task-release.js';

let pool;
try {
  const config = JSON.parse(readFileSync(new URL('./gregale-mcp.json', import.meta.url)));
  const settings = resolveMcpTaskSettings(config, { role: 'observer' });
  const input = JSON.parse(process.env.MCP_TASK_RELEASE_INPUT);
  pool = new pg.Pool({ connectionString: settings.databaseURL, max: 1, connectionTimeoutMillis: 5000, statement_timeout: 5000 });
  const inventory = mcpTaskHandlerInventory(mcpTaskHandlers);
  const admission = await createMcpTaskAdmissionController({ pool, namespace: settings.namespace }).status();
  const compatible = (await checkMcpTaskCompatibility({ pool, namespace: settings.namespace, handlers: mcpTaskHandlers })).ok;
  const admitted = admission.enforced && admission.versions.every(entry => entry.state !== 'allowed' || inventory.some(handler => handler.name === entry.tool && handler.version === entry.version));
  const ok = compatible && admitted && await checkMcpTaskReplacementReadiness({ pool, namespace: settings.namespace, handlers: mcpTaskHandlers, workerIDs: input.replacementWorkerIDs });
  console.log(JSON.stringify({ ok }));
  if (!ok) process.exitCode = 1;
} catch {
  console.log(JSON.stringify({ ok: false }));
  process.exitCode = 1;
} finally { await pool?.end().catch(() => {}); }
