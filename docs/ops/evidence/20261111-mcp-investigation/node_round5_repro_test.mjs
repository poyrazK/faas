// Expected-failing PostgreSQL probe for an unsafe retained-key preflight sample.
import assert from 'node:assert/strict';
import test from 'node:test';
import { randomUUID } from 'node:crypto';
import { createRequire } from 'node:module';
import { resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

const starter = process.env.MCP_INVESTIGATION_NODE_STARTER;
if (!starter) throw new Error('Set MCP_INVESTIGATION_NODE_STARTER to a dependency-installed starter copy');
const fromStarter = file => import(pathToFileURL(resolve(starter, file)).href);
const databaseURL = process.env.MCP_TASKS_TEST_DATABASE_URL;

test('preflight refuses removal of a key still needed by a retained legacy task', {
  timeout: 15000, skip: databaseURL ? false : 'set MCP_TASKS_TEST_DATABASE_URL to a disposable PostgreSQL database',
}, async t => {
  const {Pool} = createRequire(resolve(starter, 'package.json'))('pg');
  const {createPostgresMcpTaskStore} = await fromStarter('task-store.js');
  const {createMcpTaskPayloadCipher} = await fromStarter('task-crypto.js');
  const schema = `investigation_mcp_${randomUUID().replaceAll('-', '')}`;
  const admin = new Pool({connectionString: databaseURL, max: 1, connectionTimeoutMillis: 3000});
  let pool;
  let createdSchema = false;
  try {
    await admin.query(`CREATE SCHEMA ${schema}`);
    createdSchema = true;
    pool = new Pool({connectionString: databaseURL, max: 4, connectionTimeoutMillis: 3000,
      statement_timeout: 5000, options: `-c search_path=${schema}`});
    const namespace = `probe-${randomUUID()}`;
    const currentKey = 'current-owner-0123456789012345678901234567890123456789';
    const previousKey = 'previous-owner-012345678901234567890123456789012345678';
    const current = {id:'initial',key:currentKey};
    const previous = {id:'previous',key:previousKey};
    const settings = {pool, namespace, ownerKey:currentKey, ttlMs:60000};
    const retained = createPostgresMcpTaskStore({...settings, nativeOwnerKeys:[current, previous]});
    await retained.migrate({admissionHandlers:[{name:'build_report',version:'1'}]});
    const tasks = [];
    for (let i=0;i<2;i++) tasks.push(await retained.create({toolName:'build_report',handlerVersion:'1',
      args:{report:'fixture'},authMode:'open'}));
    tasks.sort((a,b)=>a.task_id.localeCompare(b.task_id));
    const olderPayloadTask = tasks[1];
    // Represent a legitimate retained v1 payload from an earlier owner-key
    // generation. Its envelope carries no ID; readers need the retained key.
    const oldCipher = createMcpTaskPayloadCipher(previousKey, undefined, [previous]);
    const aad = `gregale-mcp-task:v1:${namespace}:${olderPayloadTask.task_id}:arguments`;
    const oldPayload = oldCipher.encrypt(Buffer.from(JSON.stringify({report:'retained'})),aad);
    assert.equal(oldPayload[0], 1);
    await pool.query("UPDATE gregale_mcp_tasks SET arguments_encrypted=$3,created_at=clock_timestamp()-interval '1 minute' WHERE namespace=$1 AND task_id=$2",
      [namespace,olderPayloadTask.task_id,oldPayload]);
    await retained.initialize(); // Control: both rows are readable with the retained key.
    const removed = createPostgresMcpTaskStore({...settings,nativeOwnerKeys:[current]});
    let preflightError;
    try { await removed.initialize(); } catch (error) { preflightError=error; }
    let claimError;
    try { await removed.claim(3,30000); } catch (error) { claimError=error; }
    const rows = (await pool.query('SELECT status,attempt_count FROM gregale_mcp_tasks WHERE namespace=$1 ORDER BY created_at', [namespace])).rows;
    const recovered = await retained.claim(3,30000);
    assert.equal(recovered.task_id,olderPayloadTask.task_id);
    assert.equal(recovered.arguments.report,'retained');
    await retained.complete(recovered.task_id,recovered.lease_token,{content:[]});
    t.diagnostic(`preflight accepted missing key=${!preflightError}; claim authentication failed=${!!claimError}; retained queue=${JSON.stringify(rows)}; restoring retained key recovers the task=true`);
    assert.ok(claimError, 'fixture must establish that a retained task still needs the removed key');
    assert.ok(preflightError, 'startup succeeds because only the first legacy payload is authenticated; the next claim cannot process retained work');
  } finally {
    await pool?.end();
    if (createdSchema) await admin.query(`DROP SCHEMA ${schema} CASCADE`);
    await admin.end();
  }
});
