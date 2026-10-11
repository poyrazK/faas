// Database probes for disposable native hosting qualification. No payloads or credentials are printed.
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { pathToFileURL } from 'node:url';
import { resolve } from 'node:path';

export async function rolloutProbe({ starter, action, env = process.env, tasks = {} }) {
  if (!['roles', 'seed', 'input', 'snapshot'].includes(action)) throw new Error('Unknown qualification action');
  const require = createRequire(resolve(starter, 'package.json'));
  const { Pool } = require('pg');
  const load = name => import(pathToFileURL(resolve(starter, name)).href);
  const { resolveMcpTaskSettings } = await load('task-runtime.js');
  const { createPostgresMcpTaskStore } = await load('task-store.js');
  const { checkMcpTaskSchema } = await load('task-schema.js');
  const config = JSON.parse(readFileSync(resolve(starter, 'gregale-mcp.json')));
  const settings = resolveMcpTaskSettings(config, { env: { ...env, DATABASE_URL: env.MCP_QUAL_RUNTIME_DATABASE_URL }, role: 'worker' });
  if (!settings.namespace.startsWith('mcp-qual-')) throw new Error('Dedicated qualification namespace required');
  const pools = [];
  const poolFor = url => { const pool = new Pool({ connectionString: url, max: 2, connectionTimeoutMillis: 5000, statement_timeout: 5000 }); pools.push(pool); return pool; };
  try {
    const pool = poolFor(settings.databaseURL);
    const store = createPostgresMcpTaskStore({ pool, ...settings });
    const authInfo = { clientId: 'qualification', resource: new URL('https://qualification.invalid/mcp'), extra: { subject: 'disposable-qualification-owner' } };
    if (action === 'roles') {
      const actors = {};
      let endpoint;
      for (const [role, binding] of Object.entries({ runtime: 'MCP_QUAL_RUNTIME_DATABASE_URL', observer: 'MCP_QUAL_OBSERVER_DATABASE_URL', operator: 'MCP_QUAL_OPERATOR_DATABASE_URL', migration: 'MCP_TASK_MIGRATION_DATABASE_URL' })) {
        if (!env[binding]) throw new Error('Missing qualification role binding');
        const url = new URL(env[binding]);
        const identity = `${url.hostname}:${url.port || '5432'}${url.pathname}`;
        if (endpoint && identity !== endpoint) throw new Error('Qualification bindings must target the same database endpoint');
        endpoint = identity;
        const rolePool = poolFor(env[binding]);
        const row = (await rolePool.query("SELECT current_user AS role, current_schema() AS schema, has_schema_privilege(current_schema(), 'CREATE') AS ddl")).rows[0];
        actors[role] = row;
        if (role !== 'migration') {
          if (row.ddl) throw new Error('Runtime, observer and operator must not have schema CREATE');
          await checkMcpTaskSchema({ pool: rolePool, namespace: settings.namespace, role });
        }
      }
      if (new Set(Object.values(actors).map(actor => actor.role)).size !== 4 || new Set(Object.values(actors).map(actor => actor.schema)).size !== 1 || !actors.migration.ddl) throw new Error('Four distinct database accounts in one trusted schema required');
      await store.initialize({ admissionHandlers: [{ name: 'build_report', version: '1' }] });
      return { ok: true, actors };
    }
    if (action === 'seed') {
      await store.initialize({ admissionHandlers: [{ name: 'build_report', version: '1' }] });
      const ids = {};
      for (const scenario of ['running', 'retrying', 'input-required']) {
        const task = await store.create({ toolName: 'build_report', handlerVersion: '1', args: { report: scenario, steps: 1 }, authInfo, authMode: 'external-oauth', inputMethods: ['elicitation/create:form'] });
        ids[scenario] = task.task_id;
      }
      return { ok: true, tasks: ids };
    }
    if (Object.keys(tasks).sort().join(',') !== 'input-required,retrying,running' || Object.values(tasks).some(id => !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(id))) throw new Error('Invalid probe Task IDs');
    if (action === 'input') {
      const ok = await store.updateInputs({ taskID: tasks['input-required'], authInfo, authMode: 'external-oauth', inputResponses: { approval: { action: 'accept' } } });
      return { ok };
    }
    const snapshots = {};
    for (const [scenario, id] of Object.entries(tasks)) {
      const task = await store.get({ taskID: id, authInfo, authMode: 'external-oauth' });
      if (!task) throw new Error('Qualification Task missing or expired');
      const meta = (await pool.query('SELECT next_attempt_at > clock_timestamp() AS delayed FROM gregale_mcp_tasks WHERE namespace=$1 AND task_id=$2', [settings.namespace, id])).rows[0];
      let candidateResult = false, runtimeRole;
      if (task.status === 'completed') {
        const result = JSON.parse(task.result.content[0].text);
        candidateResult = result.qualification === true && result.scenario === scenario && result.generation === 'candidate';
        runtimeRole = result.role;
      }
      snapshots[scenario] = { status: task.status, attempts: task.attempt_count, delayed: meta.delayed, candidateResult, runtimeRole };
    }
    return { ok: true, snapshots };
  } finally { await Promise.all(pools.map(pool => pool.end())); }
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  try {
    const [starter, action] = process.argv.slice(2);
    console.log(JSON.stringify(await rolloutProbe({ starter, action, tasks: JSON.parse(process.env.MCP_QUAL_TASK_IDS || '{}') })));
  } catch {
    console.log(JSON.stringify({ ok: false, detail: 'Qualification probe failed; check dedicated namespace, schema, role bindings and fixture Task state' }));
    process.exitCode = 1;
  }
}
