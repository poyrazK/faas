import { readFileSync } from 'node:fs';
import pg from 'pg';
import { mcpTaskHandlers } from './tasks.js';
import { checkMcpTaskCompatibility } from './task-compatibility.js';
import { resolveMcpTaskSettings } from './task-runtime.js';
import { createMcpTaskPayloadCipher } from './task-crypto.js';
import { createMcpTaskQueueObserver } from './task-store.js';

// Read-only preflight: no schema initialization, claims, or payload output.
const report = { ok: true, checks: [] };
function add(name, status, detail) {
  report.checks.push({ name, status, detail });
  if (status !== 'passed') report.ok = false;
}
let pool;
let stage = 'configuration';
try {
  const config = JSON.parse(readFileSync(new URL('./gregale-mcp.json', import.meta.url)));
  const settings = resolveMcpTaskSettings(config, { role: 'worker' });
  const cipher = createMcpTaskPayloadCipher(settings.ownerKey, settings.encryptionKeys);
  add('configuration', 'passed', 'Task bindings and key configuration are available');
  stage = 'database_schema_and_permissions';
  pool = new pg.Pool({ connectionString: settings.databaseURL, max: 1, connectionTimeoutMillis: 5000, statement_timeout: 10000 });
  const client = await pool.connect();
  try {
    await client.query('BEGIN READ ONLY');
    const tables = ['gregale_mcp_tasks', 'gregale_mcp_task_fairness', 'gregale_mcp_task_workers', 'gregale_mcp_task_crypto_keys'];
    for (const table of tables) {
      const result = await client.query(`SELECT to_regclass($1) IS NOT NULL AS present`, [table]);
      if (!result.rows[0].present) throw new Error('schema');
      const permissions = await client.query(`SELECT has_table_privilege(current_user, $1, 'SELECT') AND has_table_privilege(current_user, $1, 'INSERT') AND has_table_privilege(current_user, $1, 'UPDATE') AND ($1 = 'gregale_mcp_task_crypto_keys' OR has_table_privilege(current_user, $1, 'DELETE')) AS allowed`, [table]);
      if (!permissions.rows[0].allowed) throw new Error('permissions');
    }
    const sequence = await client.query("SELECT has_sequence_privilege(current_user, 'gregale_mcp_task_claim_order_seq', 'USAGE') AS allowed");
    if (!sequence.rows[0].allowed) throw new Error('permissions');
    // Resolve every column used by the runtime, without reading a Task payload.
    await client.query('SELECT namespace, task_id, owner_hash, tool_name, handler_version, arguments_encrypted, result_encrypted, error_encrypted, input_state_encrypted, status, created_at, updated_at, expires_at, attempt_count, resume_pending, next_attempt_at, lease_token, lease_expires_at, cancel_requested_at, input_methods FROM gregale_mcp_tasks LIMIT 0');
    add('database_schema_and_permissions', 'passed', 'Runtime tables, columns and DML/sequence privileges are available');
    stage = 'retained_handler_coverage';
    const compatibility = await checkMcpTaskCompatibility({ pool: client, namespace: settings.namespace, handlers: mcpTaskHandlers });
    report.handlerCompatibility = compatibility;
    add(stage, compatibility.ok ? 'passed' : 'failed', compatibility.ok ? 'Candidate handlers cover all retained nonterminal Tasks' : 'Retain missing handler versions until the reported Tasks finish or expire');
    stage = 'encryption_keys';
    const registered = await client.query('SELECT key_id, key_fingerprint FROM gregale_mcp_task_crypto_keys WHERE namespace = $1', [settings.namespace]);
    const known = new Map(registered.rows.map(row => [row.key_id, row.key_fingerprint]));
    for (const [id, fingerprint] of cipher.fingerprints) {
      if (!known.has(id) || !known.get(id).equals(fingerprint)) throw new Error('keys');
    }
    const required = await client.query(`SELECT DISTINCT ON (key_id) key_id, task_id::text, field, payload FROM (
      SELECT task_id, encrypted.field, encrypted.payload,
        CASE WHEN get_byte(encrypted.payload, 0) = 1 THEN 'legacy'
        WHEN get_byte(encrypted.payload, 0) = 2 THEN convert_from(substring(encrypted.payload FROM 3 FOR get_byte(encrypted.payload, 1)), 'UTF8') ELSE '@invalid' END AS key_id
      FROM gregale_mcp_tasks CROSS JOIN LATERAL (VALUES ('arguments', arguments_encrypted), ('result', result_encrypted), ('error', error_encrypted), ('input-state', input_state_encrypted)) AS encrypted(field, payload)
      WHERE namespace = $1 AND expires_at > clock_timestamp() AND encrypted.payload IS NOT NULL
    ) AS encrypted ORDER BY key_id, task_id, field`, [settings.namespace]);
    for (const row of required.rows) cipher.decrypt(row.payload, `gregale-mcp-task:v1:${settings.namespace}:${row.task_id}:${row.field}`);
    add('encryption_keys', 'passed', 'Configured fingerprints match and retained payload key versions are readable');
    await client.query('ROLLBACK');
  } finally {
    await client.query('ROLLBACK').catch(() => {});
    client.release();
  }
  stage = 'worker_inventory';
  const metrics = await createMcpTaskQueueObserver({ pool, namespace: settings.namespace, maxRunning: settings.maxRunning, maxRunningPerOwner: settings.maxRunningPerOwner }).queueMetrics();
  if (metrics.activeWorkers > 0) {
    add('worker_inventory', 'passed', 'Live worker registrations are available');
    add('handler_coverage', metrics.unsupportedHandlerTasks > 0 ? 'failed' : 'passed', metrics.unsupportedHandlerTasks > 0 ? 'Deploy compatible handler versions for pending work' : 'No currently eligible pending work lacks a live handler version');
  } else {
    add('worker_inventory', 'unknown', 'No live worker registration; start a worker to verify handler coverage');
  }
  add('rollout_consistency', 'passed', 'This process matches persisted key fingerprints; run preflight on every writer during a coordinated rollout');
} catch {
  const guidance = {
    configuration: 'Provide enabled Task configuration, PostgreSQL binding, stable owner secret, valid key ring and MCP_TASK_NAMESPACE',
    database_schema_and_permissions: 'Check PostgreSQL connectivity, initialized runtime schema and required table/sequence privileges',
    retained_handler_coverage: 'Check candidate handler registry and retained Task schema; do not retire a handler while coverage is unknown',
    encryption_keys: 'Check the stable owner secret, registered key fingerprints and every retained payload key version',
    worker_inventory: 'Check the worker registry schema and queue observer database access',
  };
  add(stage, 'failed', guidance[stage]);
} finally {
  await pool?.end().catch(() => {});
}
console.log(JSON.stringify(report));
process.exitCode = report.ok ? 0 : 1;
