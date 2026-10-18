import test from 'node:test'
import http from 'node:http'
import { browserInspector } from './browser-inspector.mjs'
import assert from 'node:assert/strict'
import net from 'node:net'
import { readFile, writeFile, mkdir, mkdtemp, symlink, rm } from 'node:fs/promises'
import { spawn } from 'node:child_process'
import { once } from 'node:events'
import { pathToFileURL } from 'node:url'
import { createRequire } from 'node:module'
import { join } from 'node:path'
import { randomUUID } from 'node:crypto'
import { tmpdir } from 'node:os'
import { command } from './staging/canary.mjs'
import { verifyTeamOrderSummary } from './team-order-summary.mjs'
const enabled = Boolean(process.env.DATA_API_TEST_DATABASE_URL && process.env.DATA_API_POSTGREST_BIN && process.env.DATA_API_RUNTIME_DIR)

async function port() {
  const server = net.createServer()
  await new Promise(r => server.listen(0, '127.0.0.1', r))
  const value = server.address().port
  await new Promise(r => server.close(r))
  return value
}

test('team orders enforce membership, idempotency, concurrency and real dashboard recovery', { skip: !enabled, timeout: 180000 }, async t => {
  const runtime = pathToFileURL(process.env.DATA_API_RUNTIME_DIR + '/')
  const require = createRequire(new URL('package.json', runtime)), pg = require('pg')
  const { generateKeyPair, exportJWK, createLocalJWKSet, SignJWT } = await import(pathToFileURL(require.resolve('jose')))
  const { runtimeConfig } = await import(new URL('config.mjs', runtime))
  const { inspect, fingerprint, generate } = await import(new URL('types.mjs', runtime))
  const { createServer, tokenVerifier } = await import(new URL('server.mjs', runtime))
  const suffix = `${process.pid}_${Date.now()}`, database = `teams_${suffix}`, role = `teams_login_${suffix}`
  const admin = new pg.Client({ connectionString: process.env.DATA_API_TEST_DATABASE_URL })
  await admin.connect()
  let owner, child, gateway
  t.after(async () => {
    if (gateway) { gateway.closeAllConnections(); await new Promise(r => gateway.close(r)) }
    if (child && child.exitCode === null) { child.kill(); await once(child, 'exit') }
    await owner?.end()
    await admin.query(`DROP DATABASE IF EXISTS "${database}" WITH (FORCE)`)
    await admin.query(`DROP ROLE IF EXISTS "${role}"`)
    await admin.end()
  })
  await admin.query(`CREATE ROLE "${role}" LOGIN NOINHERIT NOCREATEDB NOCREATEROLE NOBYPASSRLS PASSWORD 'test-only'`)
  await admin.query(`CREATE DATABASE "${database}"`)
  const ownerURL = new URL(process.env.DATA_API_TEST_DATABASE_URL); ownerURL.pathname = `/${database}`
  owner = new pg.Client({ connectionString: ownerURL.toString() }); await owner.connect()
  await owner.query(await readFile(new URL('../../cmd/gregale/templates/data-api-teams/migrations/sql/0001_team_orders.sql', import.meta.url), 'utf8'))
  await owner.query(await readFile(new URL('../../cmd/gregale/templates/data-api-teams/migrations/sql/0002_team_administration.sql', import.meta.url), 'utf8'))
  await owner.query(await readFile(new URL('../../cmd/gregale/templates/data-api-teams/migrations/sql/0003_team_invitations.sql', import.meta.url), 'utf8'))
  await owner.query(await readFile(new URL('../../cmd/gregale/templates/data-api-teams/migrations/sql/0004_team_order_imports.sql', import.meta.url), 'utf8'))
  await owner.query(await readFile(new URL('../../cmd/gregale/templates/data-api-teams/migrations/sql/0005_team_order_workflows.sql', import.meta.url), 'utf8'))
  await owner.query(await readFile(new URL('../../cmd/gregale/templates/data-api-teams/migrations/sql/0006_team_order_outbox.sql', import.meta.url), 'utf8'))
  await owner.query(await readFile(new URL('../../cmd/gregale/templates/data-api-teams/migrations/sql/0007_team_delivery_controls.sql', import.meta.url), 'utf8'))
  await owner.query(await readFile(new URL('../../cmd/gregale/templates/data-api-teams/migrations/sql/0008_named_webhook_subscriptions.sql', import.meta.url), 'utf8'))
  await owner.query(await readFile(new URL('../../cmd/gregale/templates/data-api-teams/migrations/sql/0009_webhook_diagnostics.sql', import.meta.url), 'utf8'))
  await owner.query(await readFile(new URL('../../cmd/gregale/templates/data-api-teams/migrations/sql/0010_saved_views_bulk_fulfilment.sql', import.meta.url), 'utf8'))
  await owner.query(await readFile(new URL('../../cmd/gregale/templates/data-api-teams/migrations/sql/0011_shared_team_views.sql', import.meta.url), 'utf8'))
  await owner.query(await readFile(new URL('../../cmd/gregale/templates/data-api-teams/migrations/sql/0012_team_order_summary.sql', import.meta.url), 'utf8'))
  await owner.query(await readFile(new URL('../../cmd/gregale/templates/data-api-teams/seed.sql', import.meta.url), 'utf8'))
  await owner.query(`GRANT USAGE ON SCHEMA api TO "${role}";
    GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA api TO "${role}";
    GRANT USAGE ON ALL SEQUENCES IN SCHEMA api TO "${role}";
    GRANT EXECUTE ON FUNCTION api.save_team_order_once(uuid,text,uuid,integer,integer,api.order_status) TO "${role}";
    GRANT EXECUTE ON FUNCTION api.list_team_members(text,text,integer), api.get_team_change_receipt(text,uuid), api.change_team_member_once(uuid,text,text,uuid,api.team_role), api.transfer_team_ownership_once(uuid,text,text,uuid) TO "${role}";
    GRANT EXECUTE ON FUNCTION api.create_team_invitation_once(uuid,text,text,api.team_role,timestamptz), api.revoke_team_invitation_once(uuid,text), api.accept_team_invitation_once(uuid,text,text), api.team_invitation_identity(), api.start_team_order_import_once(uuid,text,text,jsonb), api.advance_team_order_import_once(uuid,text,integer), api.transition_team_order_once(uuid,text,uuid,integer,api.order_status), api.claim_team_order_webhook(text,uuid), api.finish_team_order_webhook(text,uuid,uuid,boolean,integer), api.replay_team_order_webhook(text,uuid,integer), api.get_team_delivery_health(text), api.get_team_delivery_operation(text,uuid), api.preview_team_delivery_replay(text,integer), api.set_team_delivery_pause_once(text,uuid,uuid,boolean), api.batch_replay_team_deliveries_once(text,uuid,jsonb), api.save_webhook_subscription_once(text,uuid,uuid,uuid,text,text[],text,boolean,boolean), api.get_webhook_subscription_receipt(text,uuid), api.claim_subscription_webhook(text,uuid,uuid), api.finish_subscription_webhook(text,uuid,uuid,uuid,boolean,integer,text), api.replay_subscription_webhook(text,uuid,uuid,integer), api.get_subscription_health(text,uuid), api.queue_webhook_test_once(text,uuid,uuid,uuid), api.get_webhook_test_receipt(text,uuid), api.save_order_view_once(text,uuid,uuid,uuid,text,api.order_status,integer,integer,text), api.get_order_view_receipt(text,uuid), api.list_view_orders(text,api.order_status,integer,integer,text,uuid,integer,integer), api.preview_team_order_fulfilment(text,jsonb), api.fulfil_team_orders_once(text,uuid,jsonb), api.get_team_bulk_order_receipt(text,uuid), api.save_shared_order_view_once(text,uuid,uuid,uuid,text,api.order_status,integer,integer,text), api.copy_shared_order_view_once(text,uuid,uuid,uuid,uuid,text), api.get_shared_order_view_receipt(text,uuid) TO "${role}"`)
  const loginURL = new URL(ownerURL); loginURL.username = role; loginURL.password = 'test-only'
  await owner.query(`GRANT EXECUTE ON FUNCTION api.summarize_team_orders(text,api.order_status,integer,integer) TO "${role}"`)
  const snapshot = await inspect(loginURL.toString(), ['api'])
  const typed = await mkdtemp(join(tmpdir(), 'team-orders-client-'))
  t.after(() => rm(typed, { recursive: true, force: true }))
  await mkdir(join(typed, 'node_modules/@gregale'), { recursive: true })
  await symlink(new URL('../../sdk/data', import.meta.url).pathname, join(typed, 'node_modules/@gregale/data'))
  await writeFile(join(typed, 'package.json'), '{"type":"module"}')
  await writeFile(join(typed, 'database.types.ts'), generate(snapshot))
  await writeFile(join(typed, 'orders.ts'), await readFile(new URL('../../cmd/gregale/templates/data-api-teams/client/src/orders.ts', import.meta.url), 'utf8'))
  await writeFile(join(typed, 'order-summary.ts'), await readFile(new URL('../../cmd/gregale/templates/data-api-teams/client/src/order-summary.ts', import.meta.url), 'utf8'))
  await writeFile(join(typed, 'administration.ts'), await readFile(new URL('../../cmd/gregale/templates/data-api-teams/client/src/administration.ts', import.meta.url), 'utf8'))
  await writeFile(join(typed, 'invitations.ts'), await readFile(new URL('../../cmd/gregale/templates/data-api-teams/client/src/invitations.ts', import.meta.url), 'utf8'))
  await writeFile(join(typed, 'imports.ts'), await readFile(new URL('../../cmd/gregale/templates/data-api-teams/client/src/imports.ts', import.meta.url), 'utf8'))
  await writeFile(join(typed, 'workflows.ts'), await readFile(new URL('../../cmd/gregale/templates/data-api-teams/client/src/workflows.ts', import.meta.url), 'utf8'))
  await writeFile(join(typed, 'deliveries.ts'), await readFile(new URL('../../cmd/gregale/templates/data-api-teams/client/src/deliveries.ts', import.meta.url), 'utf8'))
  await writeFile(join(typed, 'delivery-controls.ts'), await readFile(new URL('../../cmd/gregale/templates/data-api-teams/client/src/delivery-controls.ts', import.meta.url), 'utf8'))
  await writeFile(join(typed, 'subscriptions.ts'), await readFile(new URL('../../cmd/gregale/templates/data-api-teams/client/src/subscriptions.ts', import.meta.url), 'utf8'))
  await writeFile(join(typed, 'webhook-diagnostics.ts'), await readFile(new URL('../../cmd/gregale/templates/data-api-teams/client/src/webhook-diagnostics.ts', import.meta.url), 'utf8'))
  await writeFile(join(typed, 'order-operations.ts'), await readFile(new URL('../../cmd/gregale/templates/data-api-teams/client/src/order-operations.ts', import.meta.url), 'utf8'))
  await writeFile(join(typed, 'shared-views.ts'), await readFile(new URL('../../cmd/gregale/templates/data-api-teams/client/src/shared-views.ts', import.meta.url), 'utf8'))
  await writeFile(join(typed, 'main.ts'), await readFile(new URL('../../cmd/gregale/templates/data-api-teams/client/src/main.ts', import.meta.url), 'utf8'))
  await writeFile(join(typed,'inference.ts'),(await readFile(new URL('../../cmd/gregale/templates/data-api-teams/client/test/inference.ts',import.meta.url),'utf8')).replaceAll('../src/','./'))
  await command(new URL('../../sdk/data/node_modules/.bin/tsc', import.meta.url).pathname, ['--strict','--target','ES2022','--module','NodeNext','--lib','ES2022,DOM,DOM.Iterable','--outDir','dist','orders.ts','main.ts','inference.ts'], { cwd: typed, env: process.env })
  const config = runtimeConfig({ DATABASE_URL: loginURL.toString(), DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'orders' })
  config.snapshot = snapshot; config.functions = snapshot.functions; config.fingerprint = fingerprint(snapshot)
  const upstream = await port(), ready = await port()
  child = spawn(process.env.DATA_API_POSTGREST_BIN, [], { env: { ...process.env, ...config.postgrestEnv, PGRST_SERVER_PORT: String(upstream), PGRST_ADMIN_SERVER_PORT: String(ready) }, stdio: ['ignore', 'pipe', 'pipe'] })
  let logs = ''; child.stdout.on('data', b => { logs += b }); child.stderr.on('data', b => { logs += b })
  const { privateKey, publicKey } = await generateKeyPair('ES256'), jwk = await exportJWK(publicKey)
  const sign = (sub, tenant = 'team-a',extra={}) => new SignJWT({ sub, tenant, role: 'postgres',...extra }).setProtectedHeader({ alg: 'ES256' }).setIssuer(config.auth.issuer).setAudience(config.auth.audience).setExpirationTime('5m').sign(privateKey)
  gateway = createServer(config, tokenVerifier(config.auth, createLocalJWKSet({ keys: [jwk] })), upstream, ready, () => {})
  await new Promise(r => gateway.listen(0, '127.0.0.1', r))
  const url = `http://127.0.0.1:${gateway.address().port}`
  for (let i = 0; i < 100 && (await fetch(url + '/healthz')).status !== 200 && child.exitCode === null; i++) await new Promise(r => setTimeout(r, 50))
  assert.equal((await fetch(url + '/healthz')).status, 200, logs)
  assert.equal((await fetch(url+'/rest/v1/team_orders')).status,401)
  const { createDataClient, dataRequest } = await import('../../sdk/data/dist/index.js')
  const { teamsClient, createOrderForm } = await import(pathToFileURL(join(typed,'dist/orders.js')))
  const token=await sign('alice','shop-b'), adminToken=await sign('amy'), memberToken=await sign('ann'), otherToken=await sign('bob','shop-a')
  const alice=createDataClient({url,accessToken:token}).schema('api')
  const amy=createDataClient({url,accessToken:adminToken}).schema('api')
  const member=createDataClient({url,accessToken:memberToken}).schema('api')
  const other=createDataClient({url,accessToken:otherToken}).schema('api')
  await verifyTeamOrderSummary({ owner, pg, loginURL: loginURL.toString(), url, typed, teamsClient, token, adminToken, memberToken, otherToken })
  const save=(db,args)=>dataRequest(db.rpc('save_team_order_once',args),{timeoutMs:5000})
  const args={request_key:randomUUID(),team_id:'shop-a',order_id:randomUUID(),expected_version:0,order_total:42,order_state:'draft'}
  const concurrent=await Promise.all(Array.from({length:6},()=>save(alice,args)))
  for(const result of concurrent){assert.equal(result.error,null);assert.deepEqual(result.data,concurrent[0].data)}
  const original=concurrent[0].data[0]
  assert.equal((await owner.query('SELECT count(*)::integer AS n FROM api.team_orders WHERE id=$1',[original.id])).rows[0].n,1)
  const separate=await save(other,{...args,team_id:'shop-b',order_total:9})
  assert.equal(separate.error,null);assert.equal(separate.data[0].tenant_id,'shop-b')
  const changed=await save(alice,{...args,order_total:43});assert.equal(changed.status,409);assert.equal(changed.error.message,'idempotency_payload_conflict')
  for(const db of [member,other]) assert.equal((await save(db,{...args,request_key:randomUUID()})).status,403)
  assert.deepEqual((await other.from('team_orders').select('id').eq('tenant_id','shop-a').eq('id',original.id)).data,[])
  assert.deepEqual((await member.from('team_orders').select('id').eq('id',original.id)).data,[{id:original.id}])
  assert.equal((await member.from('team_members').insert({tenant_id:'shop-b',subject:'ann',role:'owner'}).retry(false)).status,403)
  assert.deepEqual((await member.from('team_members').update({role:'owner'}).eq('subject','ann').select().retry(false)).data,[])
  for(const db of [alice,member,other]) {
    assert.deepEqual((await db.from('team_order_receipts').select().retry(false)).data,[])
    assert.deepEqual((await db.from('team_order_receipts').delete().select().retry(false)).data,[])
    assert.deepEqual((await db.from('team_orders').update({total_cents:0}).eq('id',original.id).select().retry(false)).data,[])
  }
  const updates=await Promise.all([save(alice,{...args,request_key:randomUUID(),expected_version:1,order_total:50}),save(amy,{...args,request_key:randomUUID(),expected_version:1,order_total:60})])
  assert.deepEqual(updates.map(r=>r.status).sort(),[200,409])
  assert.equal(updates.find(r=>!r.error).data[0].version,2)
  assert.deepEqual((await save(alice,args)).data,[original],'original receipt must not change after an edit')
  const stale=await save(alice,{...args,request_key:randomUUID(),expected_version:1});assert.equal(stale.status,409);assert.equal(stale.error.message,'order_version_conflict')
  const key= randomUUID(), oid=randomUUID()
  let drop=true, rpcCount=0, actorToken=token
  const uncertainClient=teamsClient({url,accessToken:()=>actorToken,fetch:async(input,init)=>{
    const response=await fetch(input,init)
    if(String(input).includes('/rpc/')) { rpcCount++;if(drop){drop=false;await response.text();throw new TypeError('lost after commit')} }
    return response
  }},'shop-a')
  let generated=0
  const form=createOrderForm(uncertainClient,{createKey:()=>++generated===1?key:oid})
  assert.equal((await form.submit({total:'700',status:'draft'})).uncertain,true)
  const pending=form.pending
  actorToken=adminToken
  assert.equal((await form.reconcile()).uncertain,true);assert.equal(rpcCount,1);assert.equal(form.pending,pending)
  actorToken=token
  const recovered=await form.reconcile();assert.equal(recovered.outcome,'saved');assert.equal(recovered.order.id,oid);assert.equal(rpcCount,2)
  assert.equal((await owner.query('SELECT count(*)::integer AS n FROM api.team_orders WHERE id=$1',[oid])).rows[0].n,1)
  // Previously granted JWTs lose access when authoritative membership is removed.
  await owner.query("UPDATE api.team_members SET role='owner' WHERE subject='amy' AND tenant_id='shop-a'; DELETE FROM api.team_members WHERE subject='alice' AND tenant_id='shop-a'")
  assert.deepEqual((await alice.from('team_orders').select().eq('id',oid)).data,[])
  assert.equal((await save(alice,{...args,request_key:key,order_id:oid,order_total:700})).status,403)
  await owner.query("INSERT INTO api.team_members(tenant_id,subject,role) VALUES ('shop-a','alice','owner'); UPDATE api.team_members SET role='admin' WHERE subject='amy' AND tenant_id='shop-a'")

  const { teamAdministration, createMembershipForm } = await import(pathToFileURL(join(typed,'dist/administration.js')))
  const adminApi=teamAdministration(teamsClient({url,accessToken:token},'shop-a'))
  const listMembers=()=>dataRequest(alice.rpc('list_team_members',{team_id:'shop-a'}),{timeoutMs:5000})
  const change=(db,target,revision,newRole,key=randomUUID(),team='shop-a')=>dataRequest(db.rpc('change_team_member_once',{request_key:key,team_id:team,target_subject:target,expected_revision:revision,new_role:newRole}),{timeoutMs:5000})
  const currentRevision=async who=>(await owner.query('SELECT revision FROM api.team_members WHERE tenant_id=$1 AND subject=$2',['shop-a',who])).rows[0]?.revision ?? null
  assert.deepEqual((await adminApi.members('',1)).rows.map(m=>m.subject),['alice'])
  assert.deepEqual((await adminApi.members('alice',1)).rows.map(m=>m.subject),['amy'])
  for(const db of [amy,member,other]) {
    assert.equal((await dataRequest(db.rpc('list_team_members',{team_id:'shop-a'}),{timeoutMs:5000})).status,403)
    assert.equal((await change(db,'eve',null,'owner')).status,403)
    assert.deepEqual((await db.from('team_membership_events').select()).data,[])
  }
  const unregistered=await change(alice,'not-registered',null,'member');assert.equal(unregistered.status,400,`${unregistered.error?.code}: ${unregistered.error?.message}`)
  const addKey=randomUUID(),added=await change(alice,'eve',null,'member',addKey)
  assert.equal(added.error,null);assert.equal(added.data[0].action,'add')
  assert.deepEqual((await change(alice,'eve',null,'member',addKey)).data,added.data)
  assert.equal((await change(alice,'eve',null,'admin',addKey)).status,409)
  const eveToken=await sign('eve'),eve=createDataClient({url,accessToken:eveToken}).schema('api')
  assert.ok((await eve.from('team_orders').select('id').eq('tenant_id','shop-a')).data.length>0)
  assert.equal((await change(eve,'eve',added.data[0].after_revision,'owner')).status,403)
  const roleRace=await Promise.all([change(alice,'eve',added.data[0].after_revision,'admin'),change(alice,'eve',added.data[0].after_revision,'owner')])
  assert.deepEqual(roleRace.map(r=>r.status).sort(),[200,409])
  const removed=await change(alice,'eve',await currentRevision('eve'),null)
  assert.equal(removed.error,null);assert.equal(removed.data[0].action,'remove')
  assert.deepEqual((await eve.from('team_orders').select('id').eq('tenant_id','shop-a')).data,[])
  assert.equal((await change(alice,'alice',await currentRevision('alice'),'admin')).error.message,'last_team_owner')
  assert.equal((await change(alice,'alice',await currentRevision('alice'),null)).error.message,'last_team_owner')
  await assert.rejects(owner.query("DELETE FROM api.team_members WHERE tenant_id='shop-a' AND subject='alice'"),/last_team_owner/)
  // Remove/re-add and role-away/role-back cannot make an old revision valid.
  const before=await currentRevision('ann')
  assert.equal((await change(alice,'ann',before,'admin')).error,null)
  assert.equal((await change(alice,'ann',await currentRevision('ann'),'member')).error,null)
  assert.equal((await change(alice,'ann',before,null)).error.message,'membership_revision_conflict')
  // Two co-owners attempting to demote themselves cannot leave zero owners.
  assert.equal((await change(alice,'amy',await currentRevision('amy'),'owner')).error,null)
  const ownerRace=await Promise.all([change(alice,'alice',await currentRevision('alice'),'admin'),change(amy,'amy',await currentRevision('amy'),'admin')])
  assert.deepEqual(ownerRace.map(r=>r.status).sort(),[200,409])
  assert.equal((await owner.query("SELECT count(*)::integer AS n FROM api.team_members WHERE tenant_id='shop-a' AND role='owner'")).rows[0].n,1)
  await owner.query("UPDATE api.team_members SET role='owner' WHERE tenant_id='shop-a' AND subject='alice'; UPDATE api.team_members SET role='admin' WHERE tenant_id='shop-a' AND subject='amy'")
  // Queue an owner's request behind another owner's revocation. Authorization
  // must be checked after the lock, rather than cached before waiting.
  assert.equal((await change(alice,'amy',await currentRevision('amy'),'owner')).error,null)
  const amyRevision=await currentRevision('amy'),annRevision=await currentRevision('ann')
  await owner.query('BEGIN')
  try {
    await owner.query("SELECT pg_advisory_xact_lock(hashtextextended('gregale:team-admin:shop-a',0))")
    const waiting=async count=>{for(let n=0;n<50;n++){const r=await owner.query("SELECT count(*)::integer AS n FROM pg_locks l JOIN pg_stat_activity a ON a.pid=l.pid WHERE l.locktype='advisory' AND NOT l.granted AND a.datname=$1 AND a.usename=$2",[database,role]);if(r.rows[0].n>=count)return;await new Promise(r=>setTimeout(r,20))}assert.fail('administration request did not reach the tenant lock')}
    const revoke=change(alice,'amy',amyRevision,'admin');await waiting(1)
    const queued=change(amy,'ann',annRevision,'admin');await waiting(2)
    await owner.query('COMMIT')
    assert.equal((await revoke).error,null);assert.equal((await queued).status,403)
  } finally { await owner.query('ROLLBACK') }
  await owner.query('BEGIN ISOLATION LEVEL REPEATABLE READ')
  try { await assert.rejects(owner.query("UPDATE api.team_members SET role='admin' WHERE tenant_id='shop-a' AND subject='alice'"),/membership_read_committed_required/) }
  finally {await owner.query('ROLLBACK')}
  // The database trigger also protects concurrent ordinary owner SQL.
  await owner.query("UPDATE api.team_members SET role='owner' WHERE tenant_id='shop-a' AND subject='amy'")
  const secondOwner=new pg.Client({connectionString:ownerURL.toString()});await secondOwner.connect()
  try {
    const ordinary=await Promise.allSettled([owner.query("UPDATE api.team_members SET role='admin' WHERE tenant_id='shop-a' AND subject='alice'"),secondOwner.query("UPDATE api.team_members SET role='admin' WHERE tenant_id='shop-a' AND subject='amy'")])
    assert.equal(ordinary.filter(r=>r.status==='fulfilled').length,1)
    assert.equal(ordinary.find(r=>r.status==='rejected').reason.message,'last_team_owner')
    assert.equal((await owner.query("SELECT count(*)::integer AS n FROM api.team_members WHERE tenant_id='shop-a' AND role='owner'")).rows[0].n,1)
  } finally {await secondOwner.end()}
  await owner.query("UPDATE api.team_members SET role='owner' WHERE tenant_id='shop-a' AND subject='alice'; UPDATE api.team_members SET role='admin' WHERE tenant_id='shop-a' AND subject='amy'")
  // Removing oneself also permits exact-key receipt recovery without restoring
  // membership or requiring another mutation under the removed identity.
  assert.equal((await change(alice,'amy',await currentRevision('amy'),'owner')).error,null)
  let selfRemoveWrites=0
  const selfRemoveClient=teamsClient({url,accessToken:token,fetch:async(input,init)=>{
    const response=await fetch(input,init)
    if(String(input).includes('/change_team_member_once')) {selfRemoveWrites++;await response.text();throw new TypeError('self-removal response lost')}
    return response
  }},'shop-a')
  const selfRemoveForm=createMembershipForm(selfRemoveClient)
  assert.equal((await selfRemoveForm.submit({kind:'change',target:'alice',revision:await currentRevision('alice'),role:null})).uncertain,true)
  assert.equal(await currentRevision('alice'),null)
  assert.deepEqual((await alice.from('team_members').select()).data,[])
  assert.equal((await selfRemoveForm.reconcile()).outcome,'changed');assert.equal(selfRemoveWrites,1)
  assert.equal((await change(amy,'alice',null,'owner')).error,null)
  assert.equal((await change(alice,'amy',await currentRevision('amy'),'admin')).error,null)
  // A transfer response lost after commit is resolved by a read-only receipt,
  // even though its actor is now an admin and cannot execute another transfer.
  let membershipToken=token,loseTransfer=true,transferCalls=0
  const transferClient=teamsClient({url,accessToken:()=>membershipToken,fetch:async(input,init)=>{
    const response=await fetch(input,init)
    if(String(input).includes('/transfer_team_ownership_once')) { transferCalls++;if(loseTransfer){loseTransfer=false;await response.text();throw new TypeError('transfer committed but response lost')} }
    return response
  }},'shop-a')
  const transferForm=createMembershipForm(transferClient)
  assert.equal((await transferForm.submit({kind:'transfer',target:'amy',revision:await currentRevision('amy'),role:'owner'})).uncertain,true)
  const transferPending=transferForm.pending
  membershipToken=adminToken
  assert.equal((await transferForm.reconcile()).uncertain,true);assert.equal(transferCalls,1);assert.equal(transferForm.pending,transferPending)
  membershipToken=token
  const transferResolved=await transferForm.reconcile()
  assert.equal(transferResolved.outcome,'changed');assert.equal(transferResolved.event.action,'transfer');assert.equal(transferCalls,1)
  assert.equal((await currentRevision('alice'))===transferResolved.event.actor_after_revision,true)
  assert.equal((await change(alice,'ann',await currentRevision('ann'),'admin')).status,403)
  assert.deepEqual((await alice.from('team_membership_events').select()).data,[])
  assert.equal((await dataRequest(amy.rpc('transfer_team_ownership_once',{request_key:randomUUID(),team_id:'shop-a',target_subject:'alice',expected_revision:await currentRevision('alice')}),{timeoutMs:5000})).error,null)
  // Scopes are restored; direct REST cannot edit membership/identities/history.
  assert.deepEqual((await alice.from('team_members').select('subject').eq('tenant_id','shop-a')).data,[{subject:'alice'}])
  assert.deepEqual((await alice.from('team_members').update({role:'owner'}).eq('subject','ann').select().retry(false)).data,[])
  assert.equal((await alice.from('team_users').insert({subject:'forged'}).retry(false)).status,403)
  assert.deepEqual((await alice.from('team_users').select('subject')).data,[{subject:'alice'}])
  const history=await adminApi.history(undefined,1);assert.equal(history.rows.length,1);assert.ok(history.next)
  assert.notEqual((await adminApi.history(history.next,1)).rows[0].event_id,history.rows[0].event_id)
  const eventsBefore=(await owner.query('SELECT count(*)::integer AS n FROM api.team_membership_events')).rows[0].n
  assert.deepEqual((await alice.from('team_membership_events').update({after_role:'owner'}).select().retry(false)).data,[])
  assert.deepEqual((await alice.from('team_membership_events').delete().select().retry(false)).data,[])
  assert.equal((await owner.query('SELECT count(*)::integer AS n FROM api.team_membership_events')).rows[0].n,eventsBefore)
  assert.equal((await owner.query('SELECT count(*)::integer AS n FROM api.team_membership_events WHERE request_key=$1',[addKey])).rows[0].n,1)

  const { createInvitationForm, teamInvitations, invitationLink } = await import(pathToFileURL(join(typed,'dist/invitations.js')))
  const secret='a'.repeat(64)
  const invite=(db=alice,options={})=>dataRequest(db.rpc('create_team_invitation_once',{operation_id:randomUUID(),team_id:'shop-a',invite_secret:secret,invite_role:'member',valid_until:new Date(Date.now()+3600000).toISOString(),...options}),{timeoutMs:5000})
  const accept=(db,id,options={})=>dataRequest(db.rpc('accept_team_invitation_once',{operation_id:id,team_id:'shop-a',invite_secret:secret,...options}),{timeoutMs:5000})
  const revokeInvite=(db,id)=>dataRequest(db.rpc('revoke_team_invitation_once',{operation_id:id,team_id:'shop-a'}),{timeoutMs:5000})
  const newcomer=createDataClient({url,accessToken:await sign('new-idp-user')}).schema('api')
  const contender=createDataClient({url,accessToken:await sign('competing-user')}).schema('api')
  for(const db of [amy,member,other])assert.equal((await invite(db)).status,403)
  assert.equal((await invite(alice,{invite_role:'owner'})).status,400)
  assert.equal((await invite(alice,{valid_until:new Date(Date.now()+8*86400000).toISOString()})).status,400)
  const invitation=await invite();assert.equal(invitation.error,null)
  const id=invitation.data[0].invitation_id
  assert.equal((await invite(alice,{operation_id:id,valid_until:invitation.data[0].expires_at})).error,null)
  assert.equal((await invite(alice,{operation_id:id,valid_until:invitation.data[0].expires_at,invite_role:'admin'})).status,409)
  assert.deepEqual((await newcomer.from('team_invitations').select()).data,[])
  assert.deepEqual((await other.from('team_invitations').select()).data,[])
  assert.equal((await accept(newcomer,id,{invite_secret:'b'.repeat(64)})).status,409)
  assert.equal((await accept(newcomer,id,{team_id:'shop-b'})).status,409)
  assert.equal((await accept(alice,id)).error.message,'already_team_member','existing members must never have their role replaced')
  const race=await Promise.all([accept(newcomer,id),accept(contender,id)])
  assert.deepEqual(race.map(r=>r.status).sort(),[200,409])
  const winner=race[0].status===200?newcomer:contender,joined=race.find(r=>r.status===200).data[0]
  assert.equal((await accept(winner,id)).error,null)
  assert.equal((await revokeInvite(alice,id)).error.message,'invitation_already_accepted')
  assert.equal((await owner.query('SELECT role FROM api.team_members WHERE tenant_id=$1 AND subject=$2',['shop-a',joined.accepted_subject])).rows[0].role,'member')
  assert.equal((await owner.query("SELECT count(*)::integer n FROM api.team_invitation_events WHERE invitation_id=$1 AND action='accept'",[id])).rows[0].n,1)
  assert.equal((await change(alice,joined.accepted_subject,joined.accepted_revision,null)).error,null)
  assert.equal((await accept(winner,id)).error,null,'historical acceptance receipt remains resolvable after removal')
  assert.equal((await owner.query('SELECT count(*)::integer n FROM api.team_members WHERE tenant_id=$1 AND subject=$2',['shop-a',joined.accepted_subject])).rows[0].n,0,'replay must not re-grant access')
  const revoked=(await invite()).data[0].invitation_id
  assert.equal((await revokeInvite(alice,revoked)).error,null);assert.equal((await revokeInvite(alice,revoked)).error,null)
  assert.equal((await accept(newcomer,revoked)).status,409)
  const contested=(await invite()).data[0].invitation_id
  const acceptanceAndRevocation=await Promise.all([accept(newcomer,contested),revokeInvite(alice,contested)])
  assert.deepEqual(acceptanceAndRevocation.map(r=>r.status).sort(),[200,409])
  const contestRecord=(await owner.query('SELECT accepted_subject,revoked_at FROM api.team_invitations WHERE invitation_id=$1',[contested])).rows[0]
  assert.equal(Boolean(contestRecord.accepted_subject)!==Boolean(contestRecord.revoked_at),true)
  if(contestRecord.accepted_subject)assert.equal((await change(alice,contestRecord.accepted_subject,await currentRevision(contestRecord.accepted_subject),null)).error,null)
  // Expiry must be rechecked after waiting for a concurrent team operation.
  const expiresWhileWaiting=(await invite()).data[0].invitation_id
  await owner.query('BEGIN')
  let queuedAcceptance
  try {
    await owner.query("SELECT pg_advisory_xact_lock(hashtextextended('gregale:team-admin:shop-a',0))")
    queuedAcceptance=accept(newcomer,expiresWhileWaiting)
    let waiting=false
    for(let n=0;n<100;n++) { const r=await owner.query("SELECT count(*)::integer n FROM pg_locks l JOIN pg_stat_activity a ON a.pid=l.pid WHERE l.locktype='advisory' AND NOT l.granted AND a.datname=$1 AND a.usename=$2",[database,role]);if(r.rows[0].n){waiting=true;break}await new Promise(r=>setTimeout(r,10)) }
    assert.equal(waiting,true)
    await owner.query("UPDATE api.team_invitations SET expires_at=clock_timestamp()-interval '1 second' WHERE invitation_id=$1",[expiresWhileWaiting])
    await owner.query('COMMIT')
    assert.equal((await queuedAcceptance).status,409)
  } finally {await owner.query('ROLLBACK');await queuedAcceptance}
  const expired=(await invite()).data[0].invitation_id
  await owner.query("UPDATE api.team_invitations SET expires_at=clock_timestamp()-interval '1 second' WHERE invitation_id=$1",[expired])
  assert.equal((await accept(newcomer,expired)).status,409)
  const issuerRevoked=(await invite()).data[0].invitation_id
  await owner.query("UPDATE api.team_members SET role='owner' WHERE tenant_id='shop-a' AND subject='amy'; UPDATE api.team_members SET role='admin' WHERE tenant_id='shop-a' AND subject='alice'")
  assert.equal((await accept(newcomer,issuerRevoked)).status,409)
  await owner.query("UPDATE api.team_members SET role='owner' WHERE tenant_id='shop-a' AND subject='alice'; UPDATE api.team_members SET role='admin' WHERE tenant_id='shop-a' AND subject='amy'")
  const beforeInvitationEvents=(await owner.query('SELECT count(*)::integer n FROM api.team_invitation_events')).rows[0].n
  for(const db of [alice,newcomer]) {
    assert.deepEqual((await db.from('team_invitations').update({invited_role:'owner'}).select().retry(false)).data,[])
    assert.deepEqual((await db.from('team_invitations').delete().select().retry(false)).data,[])
    assert.deepEqual((await db.from('team_invitation_events').delete().select().retry(false)).data,[])
    assert.deepEqual((await db.from('team_invitation_events').update({actor:'forged'}).select().retry(false)).data,[])
    assert.equal((await db.from('team_users').insert({subject:'forged-invite-user'}).retry(false)).status,403)
  }
  assert.equal((await owner.query('SELECT count(*)::integer n FROM api.team_invitation_events')).rows[0].n,beforeInvitationEvents)
  assert.deepEqual((await newcomer.from('team_members').select()).data,[],'temporary member scope must be restored')
  assert.equal((await teamInvitations(teamsClient({url,accessToken:token},'shop-a')).list(undefined,1)).rows.length,1)
  // Acceptance after a lost response reuses the immutable secret and has one effect.
  const lostInvite=(await invite(alice,{invite_role:'admin'})).data[0].invitation_id
  const lostToken=await sign('lost-invite-user');let dropAcceptance=true,acceptCalls=0
  const lostForm=createInvitationForm(teamsClient({url,accessToken:lostToken,fetch:async(input,init)=>{
    const response=await fetch(input,init)
    if(String(input).includes('/accept_team_invitation_once')){acceptCalls++;if(dropAcceptance){dropAcceptance=false;await response.text();throw new TypeError('lost invitation response')}}
    return response
  }},'shop-a'))
  assert.equal((await lostForm.submit({kind:'accept',link:invitationLink('https://team.example','shop-a',lostInvite,secret)})).uncertain,true)
  assert.equal((await lostForm.reconcile()).outcome,'confirmed');assert.equal(acceptCalls,2)
  assert.equal((await owner.query("SELECT count(*)::integer n FROM api.team_invitation_events WHERE invitation_id=$1 AND action='accept'",[lostInvite])).rows[0].n,1)

  const {createImportForm,previewCsv,exportOrders,orderImports}=await import(pathToFileURL(join(typed,'dist/imports.js')))
  const importItems=Array.from({length:25},(_,i)=>({id:i===0?original.id:randomUUID(),total_cents:100+i,status:'draft'}))
  const importId=randomUUID()
  const startImport=(db,items=importItems,operation=importId)=>dataRequest(db.rpc('start_team_order_import_once',{operation_id:operation,team_id:'shop-a',source_label:'orders.csv',items}),{timeoutMs:5000})
  const advanceImport=(db,cursor,id=importId)=>dataRequest(db.rpc('advance_team_order_import_once',{operation_id:id,team_id:'shop-a',expected_processed:cursor}),{timeoutMs:5000})
  for(const db of [member,other])assert.equal((await startImport(db)).status,403)
  const rowsBefore=(await owner.query("SELECT count(*)::integer n FROM api.team_orders WHERE tenant_id='shop-a'")).rows[0].n
  for(const items of [[{id:randomUUID(),total_cents:-1,status:'draft'}],[{id:randomUUID(),total_cents:1,status:'paid'}],[importItems[1],importItems[1]],[{...importItems[1],unexpected:'field'}]])assert.equal((await startImport(alice,items,randomUUID())).status,400)
  const started=await startImport(alice);assert.equal(started.error,null);assert.equal(started.data[0].processed,0)
  assert.equal((await owner.query("SELECT count(*)::integer n FROM api.team_orders WHERE tenant_id='shop-a'")).rows[0].n,rowsBefore,'registration must not write orders')
  assert.equal((await startImport(alice)).error,null)
  assert.equal((await startImport(alice,[{...importItems[1],total_cents:9}])).status,409)
  const batches=await Promise.all([advanceImport(alice,0),advanceImport(alice,0)])
  for(const result of batches){assert.equal(result.error,null);assert.equal(result.data[0].processed,20);assert.equal(result.data[0].inserted,19);assert.equal(result.data[0].skipped,1)}
  assert.equal((await advanceImport(alice,21)).status,409)
  assert.equal((await advanceImport(amy,20)).status,409,'other actors cannot resume this job')
  const finalBatch=await advanceImport(alice,20);assert.equal(finalBatch.error,null);assert.equal(finalBatch.data[0].processed,25);assert.equal(finalBatch.data[0].inserted,24);assert.ok(finalBatch.data[0].completed_at)
  assert.deepEqual((await advanceImport(alice,20)).data,finalBatch.data,'old cursor must not process twice')
  assert.equal((await owner.query("SELECT total_cents FROM api.team_orders WHERE tenant_id='shop-a' AND id=$1",[original.id])).rows[0].total_cents,updates.find(r=>!r.error).data[0].total_cents,'duplicate must not overwrite an edited order')
  assert.deepEqual((await member.from('team_order_imports').select()).data,[])
  assert.deepEqual((await other.from('team_order_imports').select()).data,[])
  assert.deepEqual((await amy.from('team_order_imports').select()).data,[])
  assert.deepEqual((await alice.from('team_order_imports').update({processed:25}).select().retry(false)).data,[])
  assert.deepEqual((await alice.from('team_order_imports').delete().select().retry(false)).data,[])
  assert.deepEqual((await alice.from('team_orders').update({total_cents:0}).eq('id',importItems[1].id).select().retry(false)).data,[],'order scope restored after import')
  const actorImport=randomUUID();assert.equal((await startImport(amy,[{id:randomUUID(),total_cents:7,status:'submitted'}],actorImport)).error,null)
  assert.equal((await alice.from('team_order_imports').select('import_id').eq('import_id',actorImport)).data.length,1,'owner sees other actors in history')
  assert.equal((await advanceImport(alice,0,actorImport)).status,409,'owner history does not permit replay as another actor')
  // A queued batch must recheck role after waiting for membership revocation.
  await owner.query('BEGIN')
  let revokedBatch
  try {
    await owner.query("SELECT pg_advisory_xact_lock(hashtextextended('gregale:team-admin:shop-a',0))")
    revokedBatch=advanceImport(amy,0,actorImport)
    let waiting=false
    for(let n=0;n<100;n++){const result=await owner.query("SELECT count(*)::integer n FROM pg_locks l JOIN pg_stat_activity a ON a.pid=l.pid WHERE l.locktype='advisory' AND NOT l.granted AND a.datname=$1 AND a.usename=$2",[database,role]);if(result.rows[0].n){waiting=true;break}await new Promise(r=>setTimeout(r,10))}
    assert.equal(waiting,true)
    await owner.query("UPDATE api.team_members SET role='member' WHERE tenant_id='shop-a' AND subject='amy'")
    await owner.query('COMMIT')
    assert.equal((await revokedBatch).status,403)
    assert.equal((await owner.query('SELECT processed FROM api.team_order_imports WHERE import_id=$1',[actorImport])).rows[0].processed,0)
  }finally{await owner.query('ROLLBACK');await revokedBatch}
  await owner.query("UPDATE api.team_members SET role='admin' WHERE tenant_id='shop-a' AND subject='amy'")
  assert.equal((await advanceImport(amy,0,actorImport)).error,null)

  let loseBatch=true,batchCalls=0,importActorToken=token
  const importClient=teamsClient({url,accessToken:()=>importActorToken,fetch:async(input,init)=>{const response=await fetch(input,init);if(String(input).includes('/advance_team_order_import_once')){batchCalls++;if(loseBatch){loseBatch=false;await response.text();throw new TypeError('committed batch response lost')}}return response}},'shop-a')
  const importer=createImportForm(importClient),plan=previewCsv('amount,state\n314,submitted\n',{total:'amount',status:'state'})
  assert.equal((await importer.start('reviewed.csv',plan.items)).outcome,'confirmed')
  assert.equal((await importer.advance()).uncertain,true)
  importActorToken=adminToken;assert.equal((await importer.reconcile()).uncertain,true);assert.equal(batchCalls,1)
  importActorToken=token;assert.equal((await importer.reconcile()).outcome,'confirmed');assert.equal(importer.job.inserted,1);assert.equal(batchCalls,2)
  const refreshed=createImportForm(teamsClient({url,accessToken:token},'shop-a'))
  assert.equal((await refreshed.resume(importer.job.import_id)).outcome,'confirmed');assert.equal(refreshed.job.processed,1)
  assert.ok((await orderImports(teamsClient({url,accessToken:token},'shop-a')).history()).rows.some(row=>row.import_id===importId))
  const exported=await exportOrders(teamsClient({url,accessToken:memberToken},'shop-a'),'submitted')
  assert.ok(exported.includes(plan.items[0].id));assert.ok(!exported.includes(importItems[1].id));assert.ok(!exported.includes('shop-b'))

  const {createWorkflowForm,orderWorkflow}=await import(pathToFileURL(join(typed,'dist/workflows.js')))
  const action=(db,id,version,state,key=randomUUID())=>dataRequest(db.rpc('transition_team_order_once',{request_key:key,team_id:'shop-a',order_id:id,expected_version:version,next_state:state}),{timeoutMs:5000})
  const workflowId=randomUUID(),workflowArgs={...args,request_key:randomUUID(),order_id:workflowId,order_total:800}
  assert.equal((await save(alice,{...workflowArgs,order_state:'submitted'})).error.message,'invalid_order_transition')
  const workflowCreated=await save(alice,workflowArgs);assert.equal(workflowCreated.error,null)
  assert.equal((await save(alice,{...workflowArgs,request_key:randomUUID(),expected_version:1,order_total:900})).error,null)
  for(const db of [member,other])assert.equal((await action(db,workflowId,2,'submitted')).status,403)
  assert.equal((await action(alice,workflowId,2,'fulfilled')).error.message,'invalid_order_transition')
  assert.equal((await save(alice,{...workflowArgs,request_key:randomUUID(),expected_version:2,order_state:'submitted'})).error.message,'invalid_order_transition')
  const transitionKeys=[randomUUID(),randomUUID()]
  const statusRace=await Promise.all([action(alice,workflowId,2,'submitted',transitionKeys[0]),action(amy,workflowId,2,'submitted',transitionKeys[1])])
  assert.deepEqual(statusRace.map(r=>r.status).sort(),[200,409]);assert.equal(statusRace.find(r=>!r.error).data[0].total_cents,900)
  assert.equal((await save(alice,{...workflowArgs,request_key:randomUUID(),expected_version:3,order_total:1000,order_state:'submitted'})).error.message,'order_not_editable')
  assert.equal((await action(alice,workflowId,3,'submitted')).error.message,'order_not_editable')
  const fulfilled=await action(amy,workflowId,3,'fulfilled');assert.equal(fulfilled.error,null);assert.equal(fulfilled.data[0].version,4)
  assert.equal((await action(alice,workflowId,4,'submitted')).error.message,'invalid_order_transition')
  assert.equal((await action(alice,workflowId,3,'fulfilled')).error.message,'order_version_conflict')
  const winningIndex=statusRace[0].status===200?0:1,winningDb=winningIndex===0?alice:amy
  assert.deepEqual((await action(winningDb,workflowId,2,'submitted',transitionKeys[winningIndex])).data,statusRace[winningIndex].data)
  assert.equal((await action(winningDb,workflowId,2,'fulfilled',transitionKeys[winningIndex])).error.message,'idempotency_payload_conflict')
  assert.equal((await owner.query('SELECT status FROM api.team_orders WHERE tenant_id=$1 AND id=$2',['shop-a',workflowId])).rows[0].status,'fulfilled')
  const workflowHistory=await orderWorkflow(teamsClient({url,accessToken:memberToken},'shop-a')).history(workflowId)
  assert.deepEqual(workflowHistory.rows.map(e=>e.action),['edit','submit','fulfil']);assert.deepEqual(workflowHistory.rows.map(e=>e.version),[2,3,4])
  assert.deepEqual((await other.from('team_order_events').select().eq('order_id',workflowId)).data,[])
  assert.deepEqual((await alice.from('team_order_events').update({actor:'forged'}).select().retry(false)).data,[])
  assert.deepEqual((await alice.from('team_order_events').delete().select().retry(false)).data,[])
  assert.equal((await alice.from('team_order_events').insert({...workflowHistory.rows[0],version:99}).retry(false)).status,403)
  await assert.rejects(owner.query("UPDATE api.team_orders SET total_cents=0,version=version+1 WHERE tenant_id='shop-a' AND id=$1",[workflowId]),/order_not_editable/)
  const lostStatusId=randomUUID(),lostStatus=(await save(alice,{...args,request_key:randomUUID(),order_id:lostStatusId})).data[0]
  let transitionToken=token,loseStatus=true,statusCalls=0
  const statusForm=createWorkflowForm(teamsClient({url,accessToken:()=>transitionToken,fetch:async(input,init)=>{const response=await fetch(input,init);if(String(input).includes('/transition_team_order_once')){statusCalls++;if(loseStatus){loseStatus=false;await response.text();throw TypeError('status response lost')}}return response}},'shop-a'))
  assert.equal((await statusForm.submit(lostStatus,'submitted')).uncertain,true)
  transitionToken=adminToken;assert.equal((await statusForm.reconcile()).uncertain,true);assert.equal(statusCalls,1)
  assert.equal((await action(amy,lostStatusId,2,'fulfilled')).error,null)
  transitionToken=token;const recoveredStatus=await statusForm.reconcile();assert.equal(recoveredStatus.outcome,'changed');assert.equal(recoveredStatus.order.status,'submitted');assert.equal(statusCalls,2)
  assert.equal((await owner.query('SELECT status FROM api.team_orders WHERE tenant_id=$1 AND id=$2',['shop-a',lostStatusId])).rows[0].status,'fulfilled')
  assert.equal((await owner.query('SELECT count(*)::integer n FROM api.team_order_events WHERE order_id=$1',[lostStatusId])).rows[0].n,2)

  const {dispatchOne,verifyWebhook}=await import('../../cmd/gregale/templates/data-api-teams/workers/order-webhooks.mjs')
  const {webhookDeliveries,createReplayForm}=await import(pathToFileURL(join(typed,'dist/deliveries.js')))
  const workerToken=await sign('order-worker','forged-tenant',{outbox_worker:true,outbox_team:'shop-a'})
  const worker=createDataClient({url,accessToken:workerToken}).schema('api')
  const claim=(db,key=randomUUID())=>dataRequest(db.rpc('claim_team_order_webhook',{team_id:'shop-a',claim_token:key}),{timeoutMs:5000})
  const finish=(db,id,key,ok,status=ok?200:503)=>dataRequest(db.rpc('finish_team_order_webhook',{team_id:'shop-a',delivery_id:id,claim_token:key,succeeded:ok,response_status:status}),{timeoutMs:5000})
  for(const db of [alice,amy,member,other])assert.equal((await claim(db)).status,403)
  const wrongWorker=createDataClient({url,accessToken:await sign('other-worker','shop-a',{outbox_worker:true,outbox_team:'shop-b'})}).schema('api')
  const stringWorker=createDataClient({url,accessToken:await sign('bad-worker','shop-a',{outbox_worker:'true',outbox_team:'shop-a'})}).schema('api')
  assert.equal((await claim(wrongWorker)).status,403);assert.equal((await claim(stringWorker)).status,403)
  assert.deepEqual((await member.from('team_order_outbox').select()).data,[])
  assert.deepEqual((await other.from('team_order_outbox').select()).data,[])
  assert.equal((await owner.query('SELECT count(*)::integer n FROM api.team_order_outbox WHERE order_id=$1',[workflowId])).rows[0].n,2,'only submit and fulfil enqueue; draft edits do not')
  const rollbackOrder=(await save(alice,{...args,order_id:randomUUID(),request_key:randomUUID()})).data[0]
  const queuedBefore=(await owner.query('SELECT count(*)::integer n FROM api.team_order_outbox')).rows[0].n
  await owner.query('BEGIN')
  try{await owner.query(`SET LOCAL ROLE "${role}"`);await owner.query("SELECT set_config('request.jwt.claims',$1,true)",[JSON.stringify({sub:'alice'})]);await owner.query("SELECT * FROM api.transition_team_order_once($1,'shop-a',$2,1,'submitted')",[randomUUID(),rollbackOrder.id]);await owner.query('ROLLBACK')}finally{await owner.query('ROLLBACK')}
  assert.equal((await owner.query('SELECT count(*)::integer n FROM api.team_order_outbox')).rows[0].n,queuedBefore,'order, event and outbox roll back atomically')
  const claimKeys=[randomUUID(),randomUUID()],claimedPair=await Promise.all(claimKeys.map(key=>claim(worker,key)))
  for(const r of claimedPair){assert.equal(r.error,null);assert.equal(r.data.length,1)}
  assert.notEqual(claimedPair[0].data[0].event_id,claimedPair[1].data[0].event_id)
  const failedDelivery=claimedPair[0].data[0],successfulDelivery=claimedPair[1].data[0]
  assert.deepEqual((await claim(worker,claimKeys[0])).data,claimedPair[0].data,'claim token replay returns one original lease')
  assert.deepEqual((await finish(worker,failedDelivery.event_id,randomUUID(),true)).data,[])
  assert.equal((await finish(worker,failedDelivery.event_id,claimKeys[0],false)).data[0].state,'pending')
  assert.equal((await finish(worker,successfulDelivery.event_id,claimKeys[1],true)).data[0].state,'delivered')
  assert.deepEqual((await finish(worker,successfulDelivery.event_id,claimKeys[1],true)).data,[],'lost ack never overwrites completed state')
  const isolate=async id=>{await owner.query("UPDATE api.team_order_outbox SET available_at=clock_timestamp()+interval '1 day' WHERE event_id<>$1 AND state='pending'",[id]);await owner.query('UPDATE api.team_order_outbox SET available_at=clock_timestamp() WHERE event_id=$1',[id])}
  for(let attempt=2;attempt<=5;attempt++){await isolate(failedDelivery.event_id);const key=randomUUID(),r=await claim(worker,key);assert.equal(r.data[0].attempts,attempt);const finished=await finish(worker,failedDelivery.event_id,key,false);assert.equal(finished.data[0].state,attempt===5?'dead':'pending');assert.ok(Date.parse(finished.data[0].available_at)>Date.now())}
  let replayToken=token,dropReplay=true,replays=0
  const ownerReplayClient=teamsClient({url,accessToken:()=>replayToken,fetch:async(input,init)=>{const response=await fetch(input,init);if(String(input).includes('/replay_team_order_webhook')){replays++;if(dropReplay){dropReplay=false;await response.text();throw TypeError('replay response lost')}}return response}},'shop-a')
  const deadRow=(await webhookDeliveries(ownerReplayClient).list()).rows.find(r=>r.event_id===failedDelivery.event_id)
  const replayForm=createReplayForm(ownerReplayClient)
  assert.equal((await replayForm.submit(deadRow)).uncertain,true)
  replayToken=adminToken;assert.equal((await replayForm.reconcile()).uncertain,true);assert.equal(replays,1)
  replayToken=token;assert.equal((await replayForm.reconcile()).outcome,'replayed');assert.equal(replays,1)
  // Real local receiver verifies HMAC over raw bytes; 503 retries preserve identity.
  const signingSecret='a'.repeat(64),received=[];let receiverStatus=503
  const receiver=http.createServer(async(req,res)=>{let raw='';for await(const chunk of req)raw+=chunk;const valid=verifyWebhook(raw,req.headers['gregale-signature'],signingSecret);received.push({valid,body:JSON.parse(raw),key:req.headers['gregale-event-id']});res.writeHead(valid?receiverStatus:401);res.end('test-only private provider body')})
  await new Promise(r=>receiver.listen(0,'127.0.0.1',r));t.after(async()=>{receiver.closeAllConnections();await new Promise(r=>receiver.close(r))})
  const workerConfig={team:'shop-a',namespace:'test-deployment',destination:`http://127.0.0.1:${receiver.address().port}/events`,secret:signingSecret}
  const workerDb={claim:async(team,key)=>{const r=await claim(worker,key);if(r.error)throw Error('claim refused');return r.data},finish:async(team,id,key,ok,status)=>{const r=await finish(worker,id,key,ok,status);if(r.error)throw Error('finish refused');return r.data}}
  await isolate(failedDelivery.event_id);assert.equal(await dispatchOne(workerConfig,workerDb),'retry-scheduled')
  receiverStatus=200;await isolate(failedDelivery.event_id)
  await mkdir(join(typed,'workers'));await mkdir(join(typed,'client'))
  await writeFile(join(typed,'workers/order-webhooks.mjs'),await readFile(new URL('../../cmd/gregale/templates/data-api-teams/workers/order-webhooks.mjs',import.meta.url),'utf8'))
  await writeFile(join(typed,'client/worker-api.mjs'),await readFile(new URL('../../cmd/gregale/templates/data-api-teams/client/worker-api.mjs',import.meta.url),'utf8'))
  const {main:runWorker}=await import(pathToFileURL(join(typed,'workers/order-webhooks.mjs')))
  assert.equal(await runWorker({DATA_API_URL:url,OUTBOX_WEBHOOK_URL:workerConfig.destination,OUTBOX_WORKER_TOKEN:workerToken,OUTBOX_TEAM:'shop-a',OUTBOX_NAMESPACE:workerConfig.namespace,OUTBOX_SIGNING_SECRET:signingSecret,OUTBOX_ALLOW_LOOPBACK:'1'}),'delivered')
  assert.equal(received.length,2);assert.ok(received.every(r=>r.valid));assert.equal(received[0].key,received[1].key);assert.deepEqual(received[0].body,received[1].body)
  assert.equal((await owner.query('SELECT generation,total_attempts FROM api.team_order_outbox WHERE event_id=$1',[failedDelivery.event_id])).rows[0].total_attempts,7)
  const attempts=await webhookDeliveries(teamsClient({url,accessToken:token},'shop-a')).attempts(failedDelivery.event_id)
  assert.equal(attempts.rows.length,7);assert.equal(attempts.rows.at(-1).outcome,'delivered');assert.ok(!JSON.stringify(attempts).includes('private provider'))
  assert.deepEqual((await worker.rpc('replay_team_order_webhook',{team_id:'shop-a',delivery_id:failedDelivery.event_id,expected_generation:1})).data,null)
  for(const db of [alice,worker]){assert.deepEqual((await db.from('team_order_outbox').update({state:'dead'}).select().retry(false)).data,[]);assert.deepEqual((await db.from('team_order_delivery_attempts').delete().select().retry(false)).data,[])}
  // Crash after external success but before acknowledgement: expiry redelivers the
  // same signed identity. Receiver deduplication is required, not inferred here.
  const crashTarget=(await owner.query("SELECT event_id FROM api.team_order_outbox WHERE state='pending' LIMIT 1")).rows[0].event_id
  await isolate(crashTarget)
  await assert.rejects(dispatchOne(workerConfig,{...workerDb,finish:async()=>{throw Error('simulated worker crash')}}))
  await owner.query("UPDATE api.team_order_outbox SET lease_until=clock_timestamp()-interval '1 second' WHERE event_id=$1",[crashTarget])
  await isolate(crashTarget);assert.equal(await dispatchOne(workerConfig,workerDb),'delivered')
  assert.equal(received.at(-2).key,received.at(-1).key)
  const crashAttempts=await webhookDeliveries(teamsClient({url,accessToken:token},'shop-a')).attempts(crashTarget);assert.deepEqual(crashAttempts.rows.map(r=>r.outcome),['expired','delivered'])
  // A final failed/crashed event remains available for owner dashboard replay.
  const browserDeadEvent=(await owner.query("SELECT event_id FROM api.team_order_outbox WHERE state='pending' LIMIT 1")).rows[0].event_id
  for(let n=1;n<=5;n++){await isolate(browserDeadEvent);const key=randomUUID(),r=await claim(worker,key);assert.equal(r.data[0].event_id,browserDeadEvent);if(n<5)await finish(worker,browserDeadEvent,key,false);else{await owner.query("UPDATE api.team_order_outbox SET lease_until=clock_timestamp()-interval '1 second' WHERE event_id=$1",[browserDeadEvent]);assert.deepEqual((await finish(worker,browserDeadEvent,key,true)).data,[])}}
  await claim(worker)
  assert.equal((await owner.query('SELECT state FROM api.team_order_outbox WHERE event_id=$1',[browserDeadEvent])).rows[0].state,'dead')

  const {deliveryControls,createDeliveryControlForm}=await import(pathToFileURL(join(typed,'dist/delivery-controls.js')))
  const controlApi=deliveryControls(teamsClient({url,accessToken:token},'shop-a'))
  const health=()=>dataRequest(alice.rpc('get_team_delivery_health',{team_id:'shop-a'}),{timeoutMs:5000})
  const pause=(db,paused,revision,key=randomUUID())=>dataRequest(db.rpc('set_team_delivery_pause_once',{team_id:'shop-a',operation_id:key,expected_revision:revision,pause_delivery:paused}),{timeoutMs:5000})
  const batch=(db,rows,key=randomUUID())=>dataRequest(db.rpc('batch_replay_team_deliveries_once',{team_id:'shop-a',operation_id:key,reviewed:rows}),{timeoutMs:5000})
  const initialHealth=await controlApi.health();assert.equal(initialHealth.paused,false);assert.equal(initialHealth.revision,null)
  assert.equal(initialHealth.pending+initialHealth.leased+initialHealth.delivered+initialHealth.dead,(await owner.query('SELECT count(*)::integer n FROM api.team_order_outbox')).rows[0].n)
  for(const db of [amy,member,other,worker]){assert.equal((await pause(db,true,null)).status,403);assert.equal((await dataRequest(db.rpc('get_team_delivery_health',{team_id:'shop-a'}),{timeoutMs:5000})).status,403);assert.equal((await batch(db,[{event_id:browserDeadEvent,generation:0}])).status,403)}
  const pendingId=(await owner.query("INSERT INTO api.team_order_outbox(tenant_id,order_id,version,payload) VALUES('shop-a',$1,99,'{}'::jsonb) RETURNING event_id",[randomUUID()])).rows[0].event_id
  const pauseKey=randomUUID();let paused,queuedPause,queuedClaim
  await owner.query('BEGIN')
  try {
    await owner.query("SELECT pg_advisory_xact_lock(hashtextextended('gregale:delivery-control:shop-a',0))")
    const waitForLocks=async count=>{for(let n=0;n<100;n++){const result=await owner.query("SELECT count(*)::integer n FROM pg_locks l JOIN pg_stat_activity a ON a.pid=l.pid WHERE l.locktype='advisory' AND NOT l.granted AND a.datname=$1 AND a.usename=$2",[database,role]);if(result.rows[0].n>=count)return;await new Promise(r=>setTimeout(r,10))}assert.fail('delivery control request did not reach barrier')}
    queuedPause=pause(alice,true,null,pauseKey);await waitForLocks(1)
    queuedClaim=claim(worker);await waitForLocks(2)
    await owner.query('COMMIT');paused=await queuedPause;assert.deepEqual((await queuedClaim).data,[])
  }finally{await owner.query('ROLLBACK');await queuedPause;await queuedClaim}
  assert.equal(paused.error,null);assert.equal(paused.data[0].action,'pause')
  const pausedRevision=paused.data[0].result.revision
  assert.deepEqual((await pause(alice,true,null,pauseKey)).data,paused.data)
  assert.equal((await pause(alice,false,pausedRevision,pauseKey)).status,409)
  assert.equal((await pause(alice,false,null)).status,409)
  assert.equal((await controlApi.health()).paused,true)
  // Paused claims neither lease ready rows nor consume retry attempts.
  assert.deepEqual((await claim(worker)).data,[])
  assert.equal((await owner.query('SELECT attempts FROM api.team_order_outbox WHERE event_id=$1',[pendingId])).rows[0].attempts,0)
  assert.ok((await controlApi.health()).oldest_pending_seconds>=0)
  const unpaused=await pause(alice,false,pausedRevision);assert.equal(unpaused.error,null)
  const liveKey=randomUUID(),live=await claim(worker,liveKey);assert.equal(live.data[0].event_id,pendingId)
  const repause=await pause(alice,true,unpaused.data[0].result.revision);assert.equal(repause.error,null)
  assert.deepEqual((await claim(worker,liveKey)).data,live.data,'paused claim-token recovery returns an existing active lease')
  assert.equal((await finish(worker,pendingId,liveKey,true)).data[0].state,'delivered','active leases can finish while paused')
  assert.deepEqual((await claim(worker)).data,[])
  // Preview is read-only and bounded. One stale row rejects the entire batch.
  const secondDead=(await owner.query("INSERT INTO api.team_order_outbox(tenant_id,order_id,version,payload,state,attempts,total_attempts) VALUES('shop-a',$1,99,'{}'::jsonb,'dead',5,5) RETURNING event_id",[randomUUID()])).rows[0].event_id
  const thirdDead=(await owner.query("INSERT INTO api.team_order_outbox(tenant_id,order_id,version,payload,state,attempts,total_attempts) VALUES('shop-a',$1,99,'{}'::jsonb,'dead',5,5) RETURNING event_id",[randomUUID()])).rows[0].event_id
  const preview=await controlApi.preview();assert.ok(preview.some(r=>r.event_id===browserDeadEvent));assert.ok(preview.length<=20)
  const beforeBatchGeneration=(await owner.query('SELECT generation FROM api.team_order_outbox WHERE event_id=$1',[browserDeadEvent])).rows[0].generation
  assert.equal((await batch(alice,[{event_id:browserDeadEvent,generation:0},{event_id:secondDead,generation:1}])).error.message,'delivery_preview_stale')
  assert.equal((await owner.query('SELECT generation FROM api.team_order_outbox WHERE event_id=$1',[browserDeadEvent])).rows[0].generation,beforeBatchGeneration,'a stale batch must roll back any earlier row reset')
  for(const rows of [[],[{event_id:secondDead,generation:0},{event_id:secondDead,generation:0}],Array.from({length:21},()=>({event_id:secondDead,generation:0}))])assert.equal((await batch(alice,rows)).status,400)
  const batchItems=[{event_id:secondDead,generation:0},{event_id:thirdDead,generation:0}],batchKeys=[randomUUID(),randomUUID()]
  const batchRace=await Promise.all(batchKeys.map(key=>batch(alice,batchItems,key)))
  assert.deepEqual(batchRace.map(r=>r.status).sort(),[200,409])
  const batchWinner=batchRace[0].status===200?0:1,batchKey=batchKeys[batchWinner],batched=batchRace[batchWinner]
  assert.equal(batched.error,null);assert.equal(batched.data[0].result.replayed.length,2)
  assert.deepEqual((await batch(alice,[...batchItems].reverse(),batchKey)).data,batched.data,'same reviewed set replays one immutable operation')
  assert.equal((await batch(alice,[{event_id:browserDeadEvent,generation:0}],batchKey)).status,409)
  assert.deepEqual((await claim(worker)).data,[],'replaying while paused does not resume delivery')
  assert.equal((await owner.query("SELECT count(*)::integer n FROM api.team_delivery_operations WHERE operation_id=$1 AND action='batch_replay'",[batchKey])).rows[0].n,1)
  // Independent owners with the same revision cannot silently overwrite settings.
  assert.equal((await change(alice,'amy',await currentRevision('amy'),'owner')).error,null)
  const pauseRaces=await Promise.all([pause(alice,false,repause.data[0].result.revision),pause(amy,false,repause.data[0].result.revision)])
  assert.deepEqual(pauseRaces.map(r=>r.status).sort(),[200,409])
  assert.equal((await change(alice,'amy',await currentRevision('amy'),'admin')).error,null)
  for(const db of [alice,worker]){assert.deepEqual((await db.from('team_delivery_controls').update({paused:true}).select().retry(false)).data,[]);assert.deepEqual((await db.from('team_delivery_operations').delete().select().retry(false)).data,[]);assert.deepEqual((await db.from('team_delivery_operations').update({actor:'forged'}).select().retry(false)).data,[])}
  assert.deepEqual((await member.from('team_delivery_operations').select()).data,[])
  assert.deepEqual((await other.from('team_delivery_operations').select()).data,[])
  // Exact receipt recovery still works after the original actor loses ownership.
  let lostControl=true,controlWrites=0
  const lostControlClient=teamsClient({url,accessToken:token,fetch:async(input,init)=>{const response=await fetch(input,init);if(String(input).includes('/set_team_delivery_pause_once')){controlWrites++;if(lostControl){lostControl=false;await response.text();throw TypeError('control response lost')}}return response}},'shop-a')
  const controlForm=createDeliveryControlForm(lostControlClient)
  assert.equal((await controlForm.pause(true,(await controlApi.health()).revision)).uncertain,true)
  assert.equal((await change(alice,'amy',await currentRevision('amy'),'owner')).error,null)
  assert.equal((await change(alice,'alice',await currentRevision('alice'),'admin')).error,null)
  assert.equal((await controlForm.reconcile()).outcome,'confirmed');assert.equal(controlWrites,1)
  assert.deepEqual((await alice.from('team_delivery_operations').select()).data,[],'former owner cannot browse operation history')
  assert.equal((await change(amy,'alice',await currentRevision('alice'),'owner')).error,null)
  assert.equal((await change(alice,'amy',await currentRevision('amy'),'admin')).error,null)
  const lastPaused=(await controlApi.health()).revision;assert.equal((await pause(alice,false,lastPaused)).error,null)
  // Keep test-only placeholder snapshots unavailable to the actual worker.
  await owner.query("UPDATE api.team_order_outbox SET available_at=clock_timestamp()+interval '1 day' WHERE event_id=ANY($1::uuid[])",[[secondDead,thirdDead]])

  if(process.env.DATA_API_BROWSER_REQUIRED==='1' && !process.env.DATA_API_CHROMIUM_BIN) throw new Error('Chromium is required')
  if(process.env.DATA_API_CHROMIUM_BIN) {
    const browserRoot=join(typed,'browser');await mkdir(browserRoot)
    const esbuild=createRequire(join(process.env.DATA_API_STARTER_DIR,'client/package.json'))('esbuild')
    await esbuild.build({entryPoints:[join(typed,'main.ts')],bundle:true,format:'esm',platform:'browser',target:'es2022',outfile:join(browserRoot,'client.js')})
    const page=http.createServer(async(req,res)=>{
      const file=req.url==='/'?'index.html':req.url==='/client.js'?'client.js':null
      if(!file){res.writeHead(404);res.end();return}
      res.writeHead(200,{'Content-Type':file.endsWith('.js')?'text/javascript':'text/html'})
      res.end(await readFile(file==='index.html'?new URL('../../cmd/gregale/templates/data-api-teams/client/index.html',import.meta.url):join(browserRoot,file)))
    })
    await new Promise(r=>page.listen(0,'127.0.0.1',r));t.after(async()=>{page.closeAllConnections();await new Promise(r=>page.close(r))})
    const origin=`http://127.0.0.1:${page.address().port}`;config.origins.push(origin)
    let browserInviteLink,browserImportId
    await browserInspector(origin,async evaluate=>{
      const wait=async expression=>{for(let n=0;n<100;n++){if(await evaluate(expression))return;await new Promise(r=>setTimeout(r,50))}assert.fail('dashboard transition timed out')}
      await wait("Boolean(document.getElementById('connect')?.onclick)")
      await evaluate(`window.savedFetch=window.fetch;window.dropRpc=false;window.fetch=async(...args)=>{const response=await window.savedFetch(...args);if(window.dropRpc && String(args[0]).includes('/rpc/')){window.dropRpc=false;await response.text();throw new TypeError('response lost')}return response}`)
      const fields={url,tenant:'shop-a',token}
      await evaluate(`for (const [id,value] of Object.entries(${JSON.stringify(fields)})) document.getElementById(id).value=value; document.getElementById('connect').click()`)
      await wait("document.getElementById('role').textContent.includes('owner') && !document.getElementById('reload').disabled")
      assert.equal(await evaluate("document.getElementById('token').value"),'')
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('total').value='-1';document.getElementById('save').click()`)
      await wait("document.getElementById('message').textContent.includes('whole cents') && !document.getElementById('save').disabled")
      // Drop the first RPC response after the real database transaction commits.
      await evaluate(`window.dropRpc=true;document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('total').value='777';document.getElementById('save').click()`)
      await wait("!document.getElementById('resolve').disabled")
      assert.equal(await evaluate("document.getElementById('url').disabled && document.getElementById('tenant').disabled && document.getElementById('save').disabled"),true)
      await evaluate(`document.getElementById('token').value=${JSON.stringify(adminToken)};document.getElementById('resolve').click()`)
      await wait("document.getElementById('message').textContent.includes('original user') && !document.getElementById('resolve').disabled")
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('resolve').click()`)
      await wait("document.getElementById('message').textContent.includes('Saved order') && document.getElementById('resolve').disabled")
      assert.equal(await evaluate("document.getElementById('token').value"),'')
      assert.equal((await owner.query("SELECT count(*)::integer AS n FROM api.team_orders WHERE total_cents=777")).rows[0].n,1)
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('deliveries-load').click()`)
      await wait(`Boolean(document.querySelector('[data-delivery="${browserDeadEvent}"]')) && !document.getElementById('deliveries-load').disabled`)
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.querySelector('[data-delivery="${browserDeadEvent}"]').click()`)
      await wait("document.getElementById('delivery-attempts').textContent.includes('expired')")
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};window.dropRpc=true;Array.from(document.querySelectorAll('[data-delivery="${browserDeadEvent}"]')).find(b=>b.textContent.includes('Replay')).click()`)
      await wait("!document.getElementById('delivery-resolve').disabled")
      assert.equal(await evaluate("document.getElementById('save').disabled && document.getElementById('tenant').disabled && document.getElementById('import-start').disabled"),true)
      await evaluate(`document.getElementById('token').value=${JSON.stringify(adminToken)};document.getElementById('delivery-resolve').click()`)
      await wait("document.getElementById('delivery-message').textContent.includes('original owner')")
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('delivery-resolve').click()`)
      await wait("document.getElementById('delivery-message').textContent.includes('generation confirmed') && document.getElementById('delivery-resolve').disabled")
      assert.equal((await owner.query('SELECT generation FROM api.team_order_outbox WHERE event_id=$1',[browserDeadEvent])).rows[0].generation,1,'read-only recovery must not replay twice')
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('delivery-health').click()`)
      await wait("document.getElementById('queue-health').textContent.includes('Running') && !document.getElementById('delivery-pause').disabled")
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};window.dropRpc=true;document.getElementById('delivery-pause').click()`)
      await wait("!document.getElementById('control-resolve').disabled")
      assert.equal(await evaluate("document.getElementById('tenant').disabled && document.getElementById('save').disabled && document.getElementById('batch-preview').disabled"),true)
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('control-resolve').click()`)
      await wait("document.getElementById('control-message').textContent.includes('pause operation confirmed') && document.getElementById('queue-health').textContent.includes('Paused')")
      // Make two reviewed test failures available for a browser batch, without
      // dispatching their placeholder snapshots to any receiver.
      await owner.query("UPDATE api.team_order_outbox SET state='dead',attempts=5 WHERE event_id=ANY($1::uuid[])",[[secondDead,thirdDead]])
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('batch-preview').click()`)
      await wait("document.getElementById('control-message').textContent.includes('2 eligible failures reviewed')")
      assert.equal(await evaluate("document.getElementById('batch-replay').disabled"),true)
      await evaluate(`document.getElementById('batch-confirm').checked=true;document.getElementById('batch-confirm').dispatchEvent(new Event('change'));document.getElementById('token').value=${JSON.stringify(token)};window.dropRpc=true;document.getElementById('batch-replay').click()`)
      await wait("!document.getElementById('control-resolve').disabled")
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('control-resolve').click()`)
      // Receipt confirmation precedes the health refresh. Wait for the next
      // action to be enabled so a click cannot be lost while that refresh runs.
      await wait("document.getElementById('control-message').textContent.includes('batch_replay operation confirmed') && document.getElementById('control-resolve').disabled && !document.getElementById('delivery-resume').disabled")
      assert.equal((await owner.query('SELECT generation FROM api.team_order_outbox WHERE event_id=$1',[secondDead])).rows[0].generation,2)
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('delivery-resume').click()`)
      await wait("document.getElementById('control-message').textContent.includes('resume operation confirmed') && document.getElementById('queue-health').textContent.includes('Running')")
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('operations-load').click()`)
      await wait("document.getElementById('delivery-operations').textContent.includes('batch_replay') && document.getElementById('delivery-operations').textContent.includes('alice')")
      const browserWorkflowId='00000000-0000-4000-8000-000000000001'
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('reload').click()`)
      await wait(`Boolean(document.querySelector('[data-edit="${browserWorkflowId}"]')) && !document.getElementById('reload').disabled`)
      await evaluate(`document.querySelector('[data-edit="${browserWorkflowId}"]').click();document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('order-submit').click()`)
      await wait("document.getElementById('workflow-message').textContent.includes('confirm its status change')")
      assert.equal((await owner.query('SELECT status FROM api.team_orders WHERE id=$1',[browserWorkflowId])).rows[0].status,'draft')
      await evaluate(`document.getElementById('workflow-confirm').checked=true;window.dropRpc=true;document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('order-submit').click()`)
      await wait("!document.getElementById('workflow-resolve').disabled")
      assert.equal(await evaluate("document.getElementById('save').disabled && document.getElementById('import-start').disabled && document.getElementById('tenant').disabled"),true)
      await evaluate(`document.getElementById('token').value=${JSON.stringify(adminToken)};document.getElementById('workflow-resolve').click()`)
      await wait("document.getElementById('workflow-message').textContent.includes('original owner or admin')")
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('workflow-resolve').click()`)
      await wait("document.getElementById('workflow-message').textContent.includes('confirmed submitted') && !document.getElementById('order-fulfil').disabled")
      assert.equal(await evaluate("document.getElementById('save').disabled && document.getElementById('total').disabled && document.getElementById('state').disabled"),true)
      await evaluate(`document.getElementById('workflow-confirm').checked=true;document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('order-fulfil').click()`)
      await wait("document.getElementById('workflow-message').textContent.includes('confirmed fulfilled') && document.getElementById('order-fulfil').disabled")
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.querySelector('[data-order-history="${browserWorkflowId}"]').click()`)
      await wait("document.getElementById('order-history').textContent.includes('fulfil')")
      assert.ok(await evaluate("document.getElementById('order-history').textContent.includes('alice') && document.getElementById('order-history').textContent.includes('submitted → fulfilled')"))
      await evaluate("document.getElementById('cancel').click()")
      const browserCsv='amount,state\n'+Array.from({length:25},(_,i)=>`${900+i},draft`).join('\n')+'\n'
      await evaluate(`document.getElementById('import-csv').value='amount,state\\n-1,draft';document.getElementById('import-columns').click();document.getElementById('import-preview').click()`)
      assert.equal(await evaluate("document.getElementById('import-start').disabled && document.getElementById('import-errors').textContent.includes('whole cents')"),true)
      const jobsBefore=(await owner.query('SELECT count(*)::integer n FROM api.team_order_imports')).rows[0].n
      await evaluate(`document.getElementById('import-csv').value=${JSON.stringify(browserCsv)};document.getElementById('import-columns').click();document.getElementById('import-preview').click()`)
      assert.equal(await evaluate("document.getElementById('import-start').disabled && document.getElementById('import-preview-message').textContent.includes('25 valid')"),true)
      assert.equal((await owner.query('SELECT count(*)::integer n FROM api.team_order_imports')).rows[0].n,jobsBefore,'preview performs no server writes')
      await evaluate(`document.getElementById('import-confirm').checked=true;document.getElementById('import-confirm').dispatchEvent(new Event('change'));document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('import-start').click()`)
      await wait("document.getElementById('import-message').textContent.includes('0/25 processed') && !document.getElementById('import-advance').disabled")
      browserImportId=await evaluate("document.getElementById('import-message').textContent.match(/[0-9a-f]{8}-[0-9a-f-]{27}/)[0]")
      await evaluate(`window.dropRpc=true;document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('import-advance').click()`)
      await wait("!document.getElementById('import-resolve').disabled")
      assert.equal(await evaluate("document.getElementById('save').disabled && document.getElementById('tenant').disabled && document.getElementById('invite-create').disabled"),true)
      await evaluate(`document.getElementById('token').value=${JSON.stringify(adminToken)};document.getElementById('import-resolve').click()`)
      await wait("document.getElementById('import-message').textContent.includes('original owner or admin')")
      assert.equal((await owner.query('SELECT processed FROM api.team_order_imports WHERE import_id=$1',[browserImportId])).rows[0].processed,20)
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('import-resolve').click()`)
      await wait("document.getElementById('import-message').textContent.includes('20/25 processed') && document.getElementById('import-resolve').disabled")
      assert.equal((await owner.query('SELECT processed FROM api.team_order_imports WHERE import_id=$1',[browserImportId])).rows[0].processed,20,'recovery must not advance a second batch')
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('import-advance').click()`)
      await wait("document.getElementById('import-message').textContent.includes('25/25 processed') && document.getElementById('import-message').textContent.includes('Complete.')")
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('imports-load').click()`)
      await wait("document.getElementById('imports').textContent.includes('pasted-orders.csv')")
      await evaluate(`window.dropRpc=true;document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('invite-create').click()`)
      await wait("!document.getElementById('invite-resolve').disabled")
      assert.equal(await evaluate("document.getElementById('save').disabled && document.getElementById('tenant').disabled && document.getElementById('invite-share').value === ''"),true)
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('invite-resolve').click()`)
      await wait("document.getElementById('invite-share').value.includes('#team=') && document.getElementById('invite-resolve').disabled")
      browserInviteLink=await evaluate("document.getElementById('invite-share').value")
      assert.equal(new URL(browserInviteLink).search,'')
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('invites-load').click()`)
      await wait("document.getElementById('invite-message').textContent.includes('invitations loaded')")
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('invite-history').click()`)
      await wait("document.getElementById('invitation-history').textContent.includes('alice')")
      await evaluate("document.getElementById('invite-clear').click()")
      assert.equal(await evaluate("document.getElementById('invite-share').value"),'')
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('members-load').click()`)
      await wait("document.querySelectorAll('#members tr').length>=3 && !document.getElementById('members-load').disabled")
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('member-subject').value='eve';document.getElementById('member-role').value='member';document.getElementById('member-save').click()`)
      await wait("document.getElementById('admin-message').textContent.includes('Confirm this access change') && !document.getElementById('member-save').disabled")
      assert.equal((await owner.query("SELECT count(*)::integer AS n FROM api.team_members WHERE tenant_id='shop-a' AND subject='eve'")).rows[0].n,0)
      await evaluate(`window.dropRpc=true;document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('member-confirm').checked=true;document.getElementById('member-save').click()`)
      await wait("!document.getElementById('member-resolve').disabled")
      assert.equal(await evaluate("document.getElementById('save').disabled && document.getElementById('resolve').disabled && document.getElementById('tenant').disabled"),true)
      await evaluate(`document.getElementById('token').value=${JSON.stringify(adminToken)};document.getElementById('member-resolve').click()`)
      await wait("document.getElementById('admin-message').textContent.includes('original user') && !document.getElementById('member-resolve').disabled")
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('member-resolve').click()`)
      await wait("document.getElementById('admin-message').textContent.includes('Access change confirmed') && document.getElementById('member-resolve').disabled")
      assert.equal((await owner.query("SELECT count(*)::integer AS n FROM api.team_members WHERE tenant_id='shop-a' AND subject='eve'")).rows[0].n,1)
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('audit-load').click()`)
      await wait("document.querySelectorAll('#audit tr').length>0 && !document.getElementById('audit-load').disabled")
      assert.ok(await evaluate("document.getElementById('audit').textContent.includes('alice') && document.getElementById('audit').textContent.includes('eve')"))
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('members-load').click()`)
      await wait("Boolean(document.querySelector('[data-member=amy]')) && !document.getElementById('members-load').disabled")
      await evaluate(`document.querySelector('[data-member=amy]').click();document.getElementById('member-confirm').checked=true;document.getElementById('token').value=${JSON.stringify(token)};window.dropRpc=true;document.getElementById('ownership-transfer').click()`)
      await wait("!document.getElementById('member-resolve').disabled")
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('member-resolve').click()`)
      await wait("document.getElementById('role').textContent.includes('admin') && document.getElementById('member-resolve').disabled")
      assert.equal(await evaluate("document.getElementById('members-load').disabled && document.getElementById('audit-load').disabled && !document.getElementById('save').disabled"),true)
      assert.equal(await evaluate("document.querySelectorAll('#audit tr').length"),0)
      assert.equal(await evaluate("document.getElementById('token').value"),'')
      await evaluate(`document.getElementById('token').value=${JSON.stringify(memberToken)};document.getElementById('connect').click()`)
      await wait("document.getElementById('role').textContent.includes('read only') && !document.getElementById('reload').disabled")
      assert.equal(await evaluate("document.getElementById('save').disabled"),true)
    })
    await owner.query("UPDATE api.team_members SET role='owner' WHERE tenant_id='shop-a' AND subject='alice'")
    await browserInspector(origin,async evaluate=>{
      const wait=async expression=>{for(let n=0;n<100;n++){if(await evaluate(expression))return;await new Promise(r=>setTimeout(r,50))}assert.fail('import resume dashboard timed out')}
      await wait("Boolean(document.getElementById('connect')?.onclick)")
      await evaluate(`document.getElementById('url').value=${JSON.stringify(url)};document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('connect').click()`)
      await wait("document.getElementById('role').textContent.includes('owner') && !document.getElementById('imports-load').disabled")
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('imports-load').click()`)
      await wait(`Boolean(document.querySelector('[data-import="${browserImportId}"]'))`)
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.querySelector('[data-import="${browserImportId}"]').click()`)
      await wait("document.getElementById('import-message').textContent.includes('25/25 processed') && document.getElementById('import-message').textContent.includes('Complete.')")
      assert.equal(await evaluate("document.getElementById('import-advance').disabled"),true)
    })
    const browserUserToken=await sign('browser-new-user')
    await browserInspector(browserInviteLink,async evaluate=>{
      const wait=async expression=>{for(let n=0;n<100;n++){if(await evaluate(expression))return;await new Promise(r=>setTimeout(r,50))}assert.fail('invitation dashboard transition timed out')}
      await wait("Boolean(document.getElementById('invite-accept')?.onclick)")
      assert.equal(await evaluate('location.hash'),'')
      assert.equal(await evaluate("document.getElementById('tenant').value"),'shop-a')
      assert.equal(await evaluate("document.getElementById('invite-link').value.includes('#team=')"),true)
      await evaluate(`document.getElementById('url').value=${JSON.stringify(url)};document.getElementById('token').value=${JSON.stringify(browserUserToken)};window.savedFetch=window.fetch;window.drop=true;window.fetch=async(...args)=>{const response=await window.savedFetch(...args);if(window.drop && String(args[0]).includes('/accept_team_invitation_once')){window.drop=false;await response.text();throw new TypeError('lost')}return response};document.getElementById('invite-accept').click()`)
      await wait("!document.getElementById('invite-resolve').disabled")
      assert.equal(await evaluate("document.getElementById('token').value === '' && document.getElementById('invite-link').value === '' && document.getElementById('tenant').disabled"),true)
      await evaluate(`document.getElementById('token').value=${JSON.stringify(browserUserToken)};document.getElementById('invite-resolve').click()`)
      await wait("document.getElementById('invite-message').textContent.includes('operation confirmed') && document.getElementById('invite-resolve').disabled")
      await evaluate(`document.getElementById('token').value=${JSON.stringify(browserUserToken)};document.getElementById('connect').click()`)
      await wait("document.getElementById('role').textContent.includes('member (read only)')")
      assert.equal(await evaluate("document.getElementById('invite-create').disabled"),true)
    })

    await browserInspector(origin,async evaluate=>{
      const wait=async expression=>{for(let n=0;n<100;n++){if(await evaluate(expression))return;await new Promise(r=>setTimeout(r,50))}assert.fail('subscription dashboard timed out: '+await evaluate("document.getElementById('subscription-message').textContent + ' / ' + document.getElementById('message').textContent"))}
      await wait("Boolean(document.getElementById('connect')?.onclick)")
      await evaluate(`window.savedFetch=window.fetch;window.drop=false;window.fetch=async(...args)=>{const response=await window.savedFetch(...args);if(window.drop && String(args[0]).includes('/rpc/')){window.drop=false;await response.text();throw new TypeError('lost')}return response};document.getElementById('url').value=${JSON.stringify(url)};document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('connect').click()`)
      await wait("document.getElementById('role').textContent.includes('owner') && !document.getElementById('subscription-save').disabled")
      await evaluate(`document.getElementById('subscription-name').value='Browser integration';document.getElementById('subscription-config').value='browser-v1';document.getElementById('subscription-enabled').checked=false;document.getElementById('token').value=${JSON.stringify(token)};window.drop=true;document.getElementById('subscription-save').click()`)
      await wait("!document.getElementById('subscription-resolve').disabled")
      assert.equal(await evaluate("document.getElementById('tenant').disabled && document.getElementById('subscription-save').disabled"),true)
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('subscription-resolve').click()`)
      await wait("document.getElementById('subscription-message').textContent.includes('Subscription saved at version 1') && document.getElementById('subscription-resolve').disabled")
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('subscriptions-load').click()`)
      await wait("document.getElementById('subscriptions').textContent.includes('Browser integration')")
      assert.equal(await evaluate("document.getElementById('subscription-test').disabled"),true)
      await evaluate(`document.getElementById('subscription-test-confirm').checked=true;document.getElementById('subscription-test-confirm').dispatchEvent(new Event('change'));document.getElementById('token').value=${JSON.stringify(token)};window.drop=true;document.getElementById('subscription-test').click()`)
      await wait("!document.getElementById('subscription-test-resolve').disabled")
      assert.equal(await evaluate("document.getElementById('subscription-save').disabled && document.getElementById('tenant').disabled && document.getElementById('subscription-test').disabled && document.getElementById('token').value===''") ,true)
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('subscription-test-resolve').click()`)
      await wait("document.getElementById('subscription-test-message').textContent.includes('queued for config browser-v1 / v1') && document.getElementById('subscription-test-resolve').disabled")
      const browserSample=(await owner.query("SELECT q.* FROM api.team_subscription_outbox q JOIN api.team_webhook_subscriptions s USING(subscription_id) WHERE s.name='Browser integration' AND q.is_test")).rows
      assert.equal(browserSample.length,1,'lost test responses must not enqueue twice')
      assert.equal(await runWorker({DATA_API_URL:url,OUTBOX_WORKER_TOKEN:await sign('browser-worker','untrusted',{outbox_worker:true,outbox_team:'shop-a',outbox_subscription:browserSample[0].subscription_id}),OUTBOX_TEAM:'shop-a',OUTBOX_NAMESPACE:workerConfig.namespace,OUTBOX_SUBSCRIPTION_ID:browserSample[0].subscription_id,OUTBOX_CONFIGS_JSON:JSON.stringify({other:{url:workerConfig.destination,secret:signingSecret}}),OUTBOX_ALLOW_LOOPBACK:'1'}),'retry-scheduled')
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('subscription-deliveries').click()`)
      await wait("document.getElementById('subscription-delivery-list').textContent.includes('Sample test') && !document.getElementById('subscription-deliveries').disabled")
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.querySelector('[data-subscription-attempts="${browserSample[0].event_id}"]').click()`)
      await wait("document.getElementById('subscription-attempt-list').textContent.includes('Configuration unavailable')")
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('subscription-history').click()`)
      await wait("document.getElementById('subscription-history-list').textContent.includes('alice') && document.getElementById('subscription-history-list').textContent.includes('browser-v1')")
      await evaluate(`document.getElementById('token').value=${JSON.stringify(memberToken)};document.getElementById('connect').click()`)
      await wait("document.getElementById('role').textContent.includes('read only')")
      assert.equal(await evaluate("document.getElementById('subscription-test').disabled && document.getElementById('subscription-history').disabled && document.getElementById('subscription-attempt-list').textContent===''") ,true)
    })

    const bulkBrowserOrders=[]
    for(const total of [750001,750002,750003]){const id=randomUUID();assert.equal((await save(alice,{...args,request_key:randomUUID(),order_id:id,order_total:total})).error,null);assert.equal((await action(alice,id,1,'submitted')).error,null);bulkBrowserOrders.push({id,total})}
    await browserInspector(origin,async evaluate=>{
      const wait=async expression=>{for(let n=0;n<100;n++){if(await evaluate(expression))return;await new Promise(r=>setTimeout(r,50))}assert.fail('saved views / bulk dashboard timed out: '+await evaluate("document.getElementById('view-message').textContent+' / '+document.getElementById('bulk-message').textContent+' / '+document.getElementById('message').textContent"))}
      await wait("Boolean(document.getElementById('connect')?.onclick)")
      await evaluate(`window.savedFetch=window.fetch;window.loseRpc=false;window.fetch=async(...args)=>{const response=await window.savedFetch(...args);if(window.loseRpc && String(args[0]).includes('/rpc/')){window.loseRpc=false;await response.text();throw new TypeError('response lost')}return response};document.getElementById('url').value=${JSON.stringify(url)};document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('connect').click()`)
      await wait("document.getElementById('role').textContent.includes('owner') && !document.getElementById('view-save').disabled")
      await evaluate(`document.getElementById('filter').value='submitted';document.getElementById('view-min').value='750000';document.getElementById('view-max').value='750010';document.getElementById('view-sort').value='total_desc';document.getElementById('view-name').value='Browser queue';document.getElementById('token').value=${JSON.stringify(token)};window.loseRpc=true;document.getElementById('view-save').click()`)
      await wait("!document.getElementById('view-resolve').disabled")
      assert.equal(await evaluate("document.getElementById('tenant').disabled && document.getElementById('view-save').disabled && document.getElementById('bulk-preview').disabled"),true)
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('view-resolve').click()`)
      await wait("document.getElementById('view-message').textContent.includes('Saved personal view Browser queue') && document.getElementById('view-resolve').disabled")
      assert.equal((await owner.query("SELECT count(*)::int n FROM api.team_order_views WHERE name='Browser queue' AND subject='alice'")).rows[0].n,1)
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('views-load').click()`)
      await wait("Boolean(document.querySelector('[data-saved-view]')) && !document.getElementById('views-load').disabled")
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.querySelector('[data-saved-view]').click()`)
      await wait("document.querySelectorAll('#rows tr').length===3 && !document.getElementById('reload').disabled")
      assert.deepEqual(await evaluate("Array.from(document.querySelectorAll('#rows tr')).map(r=>r.children[1].textContent)"),['750003','750002','750001'])
      await evaluate("for(const box of document.querySelectorAll('[data-bulk-order]')){box.checked=true;box.dispatchEvent(new Event('change'))}")
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('bulk-preview').click()`)
      await wait("document.querySelectorAll('#bulk-reviewed li').length===3 && !document.getElementById('bulk-preview').disabled")
      assert.equal(await evaluate("document.getElementById('bulk-apply').disabled"),true,'preview alone is not approval')
      assert.equal((await action(alice,bulkBrowserOrders[2].id,2,'fulfilled')).error,null)
      await evaluate(`document.getElementById('bulk-confirm').checked=true;document.getElementById('bulk-confirm').dispatchEvent(new Event('change'));document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('bulk-apply').click()`)
      await wait("document.getElementById('bulk-message').textContent.includes('Nothing was applied') && document.getElementById('bulk-resolve').disabled")
      assert.deepEqual((await owner.query('SELECT status FROM api.team_orders WHERE id=ANY($1::uuid[])',[bulkBrowserOrders.slice(0,2).map(r=>r.id)])).rows.map(r=>r.status),['submitted','submitted'])
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('reload').click()`)
      await wait("document.querySelectorAll('#rows tr').length===2 && !document.getElementById('reload').disabled")
      await evaluate("for(const box of document.querySelectorAll('[data-bulk-order]')){box.checked=true;box.dispatchEvent(new Event('change'))}")
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('bulk-preview').click()`)
      await wait("document.querySelectorAll('#bulk-reviewed li').length===2 && !document.getElementById('bulk-preview').disabled")
      await evaluate(`document.getElementById('bulk-confirm').checked=true;document.getElementById('bulk-confirm').dispatchEvent(new Event('change'));document.getElementById('token').value=${JSON.stringify(token)};window.loseRpc=true;document.getElementById('bulk-apply').click()`)
      await wait("!document.getElementById('bulk-resolve').disabled")
      assert.equal(await evaluate("document.getElementById('view-save').disabled && document.getElementById('tenant').disabled && document.getElementById('reload').disabled && document.getElementById('token').value===''") ,true)
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('bulk-resolve').click()`)
      await wait("document.getElementById('bulk-message').textContent.includes('Fulfilled 2 orders') && document.getElementById('bulk-resolve').disabled")
      assert.deepEqual((await owner.query('SELECT status,version FROM api.team_orders WHERE id=ANY($1::uuid[])',[bulkBrowserOrders.slice(0,2).map(r=>r.id)])).rows,[{status:'fulfilled',version:3},{status:'fulfilled',version:3}])
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('bulk-history').click()`)
      await wait("document.getElementById('bulk-history-list').textContent.includes('alice') && document.getElementById('bulk-history-list').textContent.includes('fulfilled 2 orders')")
      await evaluate(`document.getElementById('token').value=${JSON.stringify(memberToken)};document.getElementById('connect').click()`)
      await wait("document.getElementById('role').textContent.includes('read only') && !document.getElementById('views-load').disabled")
      assert.equal(await evaluate("document.getElementById('bulk-apply').disabled && document.getElementById('bulk-preview').disabled && !document.getElementById('view-save').disabled && document.getElementById('saved-views').textContent===''") ,true)
      await evaluate(`document.getElementById('token').value=${JSON.stringify(memberToken)};document.getElementById('views-load').click()`)
      await wait("!document.getElementById('views-load').disabled")
      assert.equal(await evaluate("document.getElementById('saved-views').textContent.includes('Browser queue')"),false)
    })

    const sharedBrowserOrders=[]
    for(const total of [770001,770002]){const id=randomUUID();assert.equal((await save(alice,{...args,request_key:randomUUID(),order_id:id,order_total:total})).error,null);assert.equal((await action(alice,id,1,'submitted')).error,null);sharedBrowserOrders.push(id)}
    await browserInspector(origin,async evaluate=>{
      const wait=async expression=>{for(let n=0;n<100;n++){if(await evaluate(expression))return;await new Promise(r=>setTimeout(r,50))}assert.fail('shared view dashboard timed out: '+await evaluate("document.getElementById('shared-view-message').textContent+' / '+document.getElementById('message').textContent"))}
      await wait("Boolean(document.getElementById('connect')?.onclick)")
      await evaluate(`window.savedFetch=window.fetch;window.drop=false;window.fetch=async(...args)=>{const response=await window.savedFetch(...args);if(window.drop && String(args[0]).includes('/rpc/')){window.drop=false;await response.text();throw new TypeError('lost')}return response};document.getElementById('url').value=${JSON.stringify(url)};document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('connect').click()`)
      await wait("document.getElementById('role').textContent.includes('owner') && !document.getElementById('shared-view-save').disabled")
      await evaluate(`document.getElementById('filter').value='submitted';document.getElementById('view-min').value='770000';document.getElementById('view-max').value='770010';document.getElementById('view-sort').value='total_desc';document.getElementById('shared-view-name').value='Browser team queue';document.getElementById('token').value=${JSON.stringify(token)};window.drop=true;document.getElementById('shared-view-save').click()`)
      await wait("!document.getElementById('shared-view-resolve').disabled")
      assert.equal(await evaluate("document.getElementById('tenant').disabled && document.getElementById('view-save').disabled && document.getElementById('shared-view-copy').disabled"),true)
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('shared-view-resolve').click()`)
      await wait("document.getElementById('shared-view-message').textContent.includes('published at version 1') && document.getElementById('shared-view-resolve').disabled")
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('shared-views-load').click()`)
      await wait("Boolean(document.querySelector('[data-shared-view]')) && !document.getElementById('shared-views-load').disabled")
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.querySelector('[data-shared-view]').click()`)
      await wait("document.querySelectorAll('#rows tr').length===2 && !document.getElementById('reload').disabled")
      assert.deepEqual(await evaluate("Array.from(document.querySelectorAll('#rows tr')).map(r=>r.children[1].textContent)"),['770002','770001'])
      await evaluate(`document.getElementById('view-min').value='770002';document.getElementById('view-min').dispatchEvent(new Event('input'));document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('shared-view-save').click()`)
      await wait("document.getElementById('shared-view-message').textContent.includes('published at version 2') && !document.getElementById('shared-view-save').disabled")
      await evaluate(`document.getElementById('token').value=${JSON.stringify(memberToken)};document.getElementById('connect').click()`)
      await wait("document.getElementById('role').textContent.includes('read only') && !document.getElementById('shared-views-load').disabled")
      assert.equal(await evaluate("document.getElementById('shared-view-save').disabled && document.getElementById('shared-view-new').disabled"),true)
      await evaluate(`document.getElementById('token').value=${JSON.stringify(memberToken)};document.getElementById('shared-views-load').click()`)
      await wait("Boolean(document.querySelector('[data-shared-view]')) && !document.getElementById('shared-views-load').disabled")
      await evaluate(`document.getElementById('token').value=${JSON.stringify(memberToken)};document.querySelector('[data-shared-view]').click()`)
      await wait("document.querySelectorAll('#rows tr').length===1 && !document.getElementById('shared-view-copy').disabled")
      const source=(await owner.query("SELECT * FROM api.team_shared_order_views WHERE name='Browser team queue'")).rows[0]
      assert.equal((await dataRequest(amy.rpc('save_shared_order_view_once',{team_id:'shop-a',request_id:randomUUID(),view_id:source.view_id,expected_revision:source.revision,view_name:source.name,status_filter:'submitted',minimum_total:770001,maximum_total:770010,sort_order:'total_desc'}),{timeoutMs:5000})).error,null)
      await evaluate(`document.getElementById('shared-copy-name').value='Private browser workflow';document.getElementById('token').value=${JSON.stringify(memberToken)};document.getElementById('shared-view-copy').click()`)
      await wait("document.getElementById('shared-view-message').textContent.includes('shared view changed') && document.getElementById('shared-view-resolve').disabled")
      await evaluate(`document.getElementById('token').value=${JSON.stringify(memberToken)};document.getElementById('shared-views-load').click()`)
      await wait("document.getElementById('shared-views').textContent.includes('v3') && !document.getElementById('shared-views-load').disabled")
      await evaluate(`document.getElementById('token').value=${JSON.stringify(memberToken)};document.querySelector('[data-shared-view]').click()`)
      await wait("document.querySelectorAll('#rows tr').length===2 && !document.getElementById('shared-view-copy').disabled")
      await evaluate(`document.getElementById('shared-copy-name').value='Private browser workflow';document.getElementById('token').value=${JSON.stringify(memberToken)};window.drop=true;document.getElementById('shared-view-copy').click()`)
      await wait("!document.getElementById('shared-view-resolve').disabled")
      assert.equal(await evaluate("document.getElementById('view-save').disabled && document.getElementById('tenant').disabled && document.getElementById('token').value===''") ,true)
      await evaluate(`document.getElementById('token').value=${JSON.stringify(memberToken)};document.getElementById('shared-view-resolve').click()`)
      await wait("document.getElementById('shared-view-message').textContent.includes('Personal copy Private browser workflow created') && document.getElementById('shared-view-resolve').disabled")
      assert.equal((await owner.query("SELECT count(*)::int n FROM api.team_order_views WHERE subject='ann' AND name='Private browser workflow'")).rows[0].n,1)
      await evaluate(`document.getElementById('token').value=${JSON.stringify(memberToken)};document.getElementById('views-load').click()`)
      await wait("document.getElementById('saved-views').textContent.includes('Private browser workflow') && !document.getElementById('views-load').disabled")
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('connect').click()`)
      await wait("document.getElementById('role').textContent.includes('owner') && !document.getElementById('views-load').disabled")
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('views-load').click()`)
      await wait("!document.getElementById('views-load').disabled")
      assert.equal(await evaluate("document.getElementById('saved-views').textContent.includes('Private browser workflow')"),false)
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('shared-views-load').click()`)
      await wait("Boolean(document.querySelector('[data-shared-view]')) && !document.getElementById('shared-views-load').disabled")
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.querySelector('[data-shared-view]').click()`)
      await wait("!document.getElementById('shared-view-history').disabled")
      await evaluate(`document.getElementById('token').value=${JSON.stringify(token)};document.getElementById('shared-view-history').click()`)
      await wait("document.querySelectorAll('#shared-view-history-list li').length===3 && document.getElementById('shared-view-history-list').textContent.includes('amy')")
      assert.equal(await evaluate("document.getElementById('shared-view-history-list').textContent.includes('Private browser workflow')"),false)
    })
    assert.ok((await owner.query('SELECT status,version FROM api.team_orders WHERE id=ANY($1::uuid[])',[sharedBrowserOrders])).rows.every(r=>r.status==='submitted'&&r.version===2),'shared view publishing and copying must not change orders')

  }
  // Named routing is tested last so existing legacy-queue acceptance stays representative.
  const {createSubscriptionForm,webhookSubscriptions}=await import(pathToFileURL(join(typed,'dist/subscriptions.js')))
  const subscriptions=webhookSubscriptions(teamsClient({url,accessToken:token},'shop-a'))
  const createNamed=async(name,filters,paused=false)=>{
    const result=await createSubscriptionForm(teamsClient({url,accessToken:token},'shop-a')).submit({revision:null,name,config_ref:'receiver-v1',filters,enabled:true,paused})
    assert.equal(result.outcome,'saved',JSON.stringify(result));return result.subscription
  }
  const submittedSub=await createNamed('Accounting',['order.submitted'])
  const bothSub=await createNamed('Warehouse',['order.submitted','order.fulfilled'],true)
  assert.equal((await subscriptions.list()).rows.filter(r=>r.enabled).length,2)
  assert.deepEqual((await member.from('team_webhook_subscriptions').select()).data,[])
  assert.equal((await createSubscriptionForm(teamsClient({url,accessToken:memberToken},'shop-a')).submit({revision:null,name:'Forbidden',config_ref:'receiver-v1',filters:['order.submitted'],enabled:true,paused:false})).outcome,'failed')
  const namedOrder=randomUUID()
  assert.equal((await save(alice,{...args,request_key:randomUUID(),order_id:namedOrder})).error,null)
  assert.equal((await action(alice,namedOrder,1,'submitted')).error,null)
  assert.equal((await action(alice,namedOrder,2,'fulfilled')).error,null)
  const namedRows=(await owner.query('SELECT * FROM api.team_subscription_outbox WHERE order_id=$1',[namedOrder])).rows
  assert.equal(namedRows.length,3,'filters fan out to two submit queues and one fulfil queue')
  assert.equal((await owner.query('SELECT count(*)::int n FROM api.team_order_outbox WHERE order_id=$1',[namedOrder])).rows[0].n,0)
  const namedWorker=async sub=>createDataClient({url,accessToken:await sign('named-worker','untrusted',{outbox_worker:true,outbox_team:'shop-a',outbox_subscription:sub})}).schema('api')
  const namedClaim=(db,sub,key=randomUUID())=>dataRequest(db.rpc('claim_subscription_webhook',{team_id:'shop-a',subscription_id:sub,claim_token:key}),{timeoutMs:5000})
  const firstWorker=await namedWorker(submittedSub.subscription_id),secondWorker=await namedWorker(bothSub.subscription_id)
  assert.equal((await namedClaim(firstWorker,bothSub.subscription_id)).status,403)
  assert.deepEqual((await namedClaim(secondWorker,bothSub.subscription_id)).data,[],'paused subscriptions accumulate without claiming')
  assert.equal((await subscriptions.health(bothSub.subscription_id)).pending,2)
  const update=await createSubscriptionForm(teamsClient({url,accessToken:token},'shop-a')).submit({id:submittedSub.subscription_id,revision:submittedSub.revision,name:submittedSub.name,config_ref:'receiver-v2',filters:submittedSub.event_types,enabled:true,paused:false})
  assert.equal(update.outcome,'saved');assert.equal(update.subscription.version,2)
  const leaseKey=randomUUID(),leased=await namedClaim(firstWorker,submittedSub.subscription_id,leaseKey)
  assert.equal(leased.error,null,JSON.stringify(leased.error));assert.equal(leased.data[0].config_ref,'receiver-v1');assert.equal(leased.data[0].subscription_version,1)
  assert.deepEqual((await namedClaim(firstWorker,submittedSub.subscription_id,leaseKey)).data,leased.data)
  const acknowledged=await dataRequest(firstWorker.rpc('finish_subscription_webhook',{team_id:'shop-a',subscription_id:submittedSub.subscription_id,delivery_id:leased.data[0].event_id,claim_token:leaseKey,succeeded:true,response_status:204}),{timeoutMs:5000})
  assert.equal(acknowledged.error,null);assert.equal(acknowledged.data[0].state,'delivered')
  assert.deepEqual((await secondWorker.from('team_subscription_outbox').select().eq('subscription_id',submittedSub.subscription_id)).data,[])
  assert.equal((await subscriptions.health(submittedSub.subscription_id)).delivered,1)
  const resumed=await createSubscriptionForm(teamsClient({url,accessToken:token},'shop-a')).submit({id:bothSub.subscription_id,revision:bothSub.revision,name:bothSub.name,config_ref:bothSub.config_ref,filters:bothSub.event_types,enabled:true,paused:false})
  assert.equal(resumed.outcome,'saved')
  let failedNamed
  for(let attempt=1;attempt<=5;attempt++){
    const key=randomUUID(),r=await namedClaim(secondWorker,bothSub.subscription_id,key)
    assert.equal(r.error,null);failedNamed=r.data[0]
    const finishResult=await dataRequest(secondWorker.rpc('finish_subscription_webhook',{team_id:'shop-a',subscription_id:bothSub.subscription_id,delivery_id:failedNamed.event_id,claim_token:key,succeeded:false,response_status:503}),{timeoutMs:5000})
    assert.equal(finishResult.error,null);assert.equal(finishResult.data[0].state,attempt===5?'dead':'pending')
    await owner.query("UPDATE api.team_subscription_outbox SET available_at=clock_timestamp()+interval '1 day' WHERE subscription_id=$1 AND event_id<>$2",[bothSub.subscription_id,failedNamed.event_id])
    await owner.query('UPDATE api.team_subscription_outbox SET available_at=clock_timestamp() WHERE event_id=$1',[failedNamed.event_id])
  }
  const replayArgs={team_id:'shop-a',subscription_id:bothSub.subscription_id,delivery_id:failedNamed.event_id,expected_generation:0}
  assert.equal((await dataRequest(member.rpc('replay_subscription_webhook',replayArgs),{timeoutMs:5000})).status,403)
  const namedReplay=await dataRequest(alice.rpc('replay_subscription_webhook',replayArgs),{timeoutMs:5000})
  assert.equal(namedReplay.error,null);assert.equal(namedReplay.data[0].generation,1)
  assert.deepEqual((await dataRequest(alice.rpc('replay_subscription_webhook',replayArgs),{timeoutMs:5000})).data,[])
  assert.deepEqual((await alice.from('team_subscription_outbox').update({state:'dead'}).select().retry(false)).data,[])
  const teamHealth=(await dataRequest(alice.rpc('get_team_delivery_health',{team_id:'shop-a'}),{timeoutMs:5000})).data
  const teamPaused=await pause(alice,true,teamHealth.revision)
  assert.equal(teamPaused.error,null)
  assert.deepEqual((await namedClaim(secondWorker,bothSub.subscription_id)).data,[],'team pause also overrides unpaused named queues')
  assert.equal((await pause(alice,false,teamPaused.data[0].result.revision)).error,null)
  receiverStatus=204
  assert.equal(await runWorker({DATA_API_URL:url,OUTBOX_WORKER_TOKEN:await sign('named-worker','untrusted',{outbox_worker:true,outbox_team:'shop-a',outbox_subscription:bothSub.subscription_id}),OUTBOX_TEAM:'shop-a',OUTBOX_NAMESPACE:workerConfig.namespace,OUTBOX_SUBSCRIPTION_ID:bothSub.subscription_id,OUTBOX_CONFIGS_JSON:JSON.stringify({'receiver-v1':{url:workerConfig.destination,secret:signingSecret}}),OUTBOX_ALLOW_LOOPBACK:'1'}),'delivered')
  assert.equal(received.at(-1).valid,true);assert.equal(received.at(-1).body.subscription_id,bothSub.subscription_id)


  const {createWebhookTestForm,webhookDiagnostics}=await import(pathToFileURL(join(typed,'dist/webhook-diagnostics.js')))
  const diagnosticClient=teamsClient({url,accessToken:token},'shop-a'),diagnostics=webhookDiagnostics(diagnosticClient)
  const ordersBefore=(await owner.query('SELECT count(*)::int n FROM api.team_orders')).rows[0].n
  const orderEventsBefore=(await owner.query('SELECT count(*)::int n FROM api.team_order_events')).rows[0].n
  let testWrites=0,lostTestArgs
  const lostTestForm=createWebhookTestForm(teamsClient({url,accessToken:token,fetch:async(input,init)=>{const response=await fetch(input,init);if(String(input).includes('/rpc/queue_webhook_test_once')){testWrites++;lostTestArgs=JSON.parse(init.body);await response.text();throw TypeError('test response lost')}return response}},'shop-a'))
  assert.equal((await lostTestForm.submit(update.subscription,true)).uncertain,true)
  assert.equal(testWrites,1)
  await owner.query("UPDATE api.team_members SET role='member' WHERE tenant_id='shop-a' AND subject='alice'")
  const recoveredTest=await lostTestForm.reconcile()
  assert.equal(recoveredTest.outcome,'queued',JSON.stringify(recoveredTest));assert.equal(testWrites,1)
  await owner.query("UPDATE api.team_members SET role='owner' WHERE tenant_id='shop-a' AND subject='alice'")
  await owner.query("UPDATE api.team_members SET role='admin' WHERE tenant_id='shop-a' AND subject='amy'")
  const testRPC=(db,a)=>dataRequest(db.rpc('queue_webhook_test_once',a),{timeoutMs:5000})
  for(const db of [amy,member,other,firstWorker])assert.equal((await testRPC(db,{...lostTestArgs,request_id:randomUUID()})).status,403)
  const sameTests=await Promise.all(Array.from({length:4},()=>testRPC(alice,lostTestArgs)))
  for(const r of sameTests){assert.equal(r.error,null);assert.deepEqual(r.data,sameTests[0].data)}
  assert.equal((await testRPC(alice,{...lostTestArgs,expected_revision:randomUUID()})).error.message,'webhook_test_payload_conflict')
  assert.equal((await testRPC(alice,{...lostTestArgs,request_id:randomUUID(),expected_revision:randomUUID()})).error.message,'subscription_revision_conflict')
  assert.equal((await testRPC(alice,{...lostTestArgs,request_id:randomUUID()})).error.message,'webhook_test_pending')
  assert.equal((await owner.query('SELECT count(*)::int n FROM api.team_orders')).rows[0].n,ordersBefore)
  assert.equal((await owner.query('SELECT count(*)::int n FROM api.team_order_events')).rows[0].n,orderEventsBefore)
  const testEvent=recoveredTest.receipt.event_id
  const testQueue=(await owner.query('SELECT * FROM api.team_subscription_outbox WHERE event_id=$1',[testEvent])).rows[0]
  assert.equal(testQueue.is_test,true);assert.equal(testQueue.payload.type,'webhook.test');assert.equal(testQueue.payload.test,true)
  assert.equal(testQueue.subscription_version,2);assert.equal(testQueue.config_ref,'receiver-v2')
  const editedAfterTest=await createSubscriptionForm(diagnosticClient).submit({id:update.subscription.subscription_id,revision:update.subscription.revision,name:update.subscription.name,config_ref:'receiver-v3',filters:update.subscription.event_types,enabled:true,paused:false})
  assert.equal(editedAfterTest.outcome,'saved')
  assert.deepEqual((await testRPC(alice,lostTestArgs)).data,sameTests[0].data,'retrying the original test never follows a later configuration edit')
  const namedEnvironment={DATA_API_URL:url,OUTBOX_WORKER_TOKEN:await sign('diagnostic-worker','untrusted',{outbox_worker:true,outbox_team:'shop-a',outbox_subscription:submittedSub.subscription_id}),OUTBOX_TEAM:'shop-a',OUTBOX_NAMESPACE:workerConfig.namespace,OUTBOX_SUBSCRIPTION_ID:submittedSub.subscription_id,OUTBOX_CONFIGS_JSON:JSON.stringify({'receiver-v1':{url:workerConfig.destination,secret:signingSecret}}),OUTBOX_ALLOW_LOOPBACK:'1'}
  const contactedBefore=received.length
  assert.equal(await runWorker(namedEnvironment),'retry-scheduled')
  assert.equal(received.length,contactedBefore,'missing bindings cannot fall back or contact another destination')
  const firstDiagnostic=await diagnostics.attempts(submittedSub.subscription_id,testEvent)
  assert.equal(firstDiagnostic.rows[0].failure_reason,'configuration_missing');assert.equal(firstDiagnostic.rows[0].http_status,null)
  await owner.query('UPDATE api.team_subscription_outbox SET available_at=clock_timestamp() WHERE event_id=$1',[testEvent])
  assert.equal(await runWorker({...namedEnvironment,OUTBOX_CONFIGS_JSON:JSON.stringify({'receiver-v2':{url:workerConfig.destination,secret:signingSecret}})}),'delivered')
  const acceptedSample=received.at(-1)
  assert.equal(acceptedSample.valid,true);assert.equal(acceptedSample.body.type,'webhook.test');assert.equal(acceptedSample.body.test,true);assert.equal(acceptedSample.body.subscription_version,2)
  assert.deepEqual(acceptedSample.body.data,{message:'Gregale webhook test',requested_by:'alice'})
  const attemptsPage=await diagnostics.attempts(submittedSub.subscription_id,testEvent)
  assert.deepEqual(attemptsPage.rows.map(r=>r.outcome),['failed','delivered']);assert.equal(attemptsPage.rows[1].http_status,204)
  assert.deepEqual((await diagnostics.attempts(bothSub.subscription_id,testEvent)).rows,[])
  assert.deepEqual((await member.from('team_subscription_attempts').select()).data,[])
  assert.deepEqual((await secondWorker.from('team_subscription_attempts').select().eq('event_id',testEvent)).data,[])
  assert.deepEqual((await member.from('team_webhook_test_receipts').select()).data,[])
  assert.deepEqual((await member.rpc('get_webhook_test_receipt',{team_id:'shop-a',operation_key:lostTestArgs.request_id})).data,[])
  for(const table of ['team_webhook_test_receipts','team_subscription_attempts'])assert.deepEqual((await alice.from(table).delete().select().retry(false)).data,[])
  const diagnosticHistory=await diagnostics.history(submittedSub.subscription_id)
  assert.deepEqual(diagnosticHistory.rows.map(r=>r.snapshot.version).sort(),[1,2,3])
  const deliveryDiagnostics=await diagnostics.deliveries(submittedSub.subscription_id)
  assert.equal(deliveryDiagnostics.rows.find(r=>r.event_id===testEvent).is_test,true)
  const pausedSampleSub=await createNamed('Paused sample',['order.submitted'],true)
  const disabledSample=await createSubscriptionForm(diagnosticClient).submit({id:pausedSampleSub.subscription_id,revision:pausedSampleSub.revision,name:pausedSampleSub.name,config_ref:pausedSampleSub.config_ref,filters:pausedSampleSub.event_types,enabled:false,paused:true})
  assert.equal(disabledSample.outcome,'saved')
  const pausedSample=await createWebhookTestForm(diagnosticClient).submit(disabledSample.subscription,true)
  assert.equal(pausedSample.outcome,'queued')
  const sampleWorker=await namedWorker(pausedSampleSub.subscription_id)
  assert.deepEqual((await namedClaim(sampleWorker,pausedSampleSub.subscription_id)).data,[],'explicit sample tests respect subscription pause even with order enqueue disabled')
  const unpausedSample=await createSubscriptionForm(diagnosticClient).submit({id:disabledSample.subscription.subscription_id,revision:disabledSample.subscription.revision,name:disabledSample.subscription.name,config_ref:disabledSample.subscription.config_ref,filters:disabledSample.subscription.event_types,enabled:false,paused:false})
  assert.equal(unpausedSample.outcome,'saved')
  const sampleLease=randomUUID(),sampleClaimed=await namedClaim(sampleWorker,pausedSampleSub.subscription_id,sampleLease)
  assert.equal(sampleClaimed.error,null);assert.equal(sampleClaimed.data[0].subscription_version,2)
  const sampleFinish={team_id:'shop-a',subscription_id:pausedSampleSub.subscription_id,delivery_id:pausedSample.receipt.event_id,claim_token:sampleLease,succeeded:false,response_status:null,failure_reason:'https://private.invalid/token'}
  assert.equal((await dataRequest(sampleWorker.rpc('finish_subscription_webhook',sampleFinish),{timeoutMs:5000})).status,400,'arbitrary diagnostics cannot persist endpoint or token values')
  const samplePausedTeam=await pause(alice,true,(await dataRequest(alice.rpc('get_team_delivery_health',{team_id:'shop-a'}),{timeoutMs:5000})).data.revision)
  assert.equal(samplePausedTeam.error,null)
  assert.deepEqual((await namedClaim(sampleWorker,pausedSampleSub.subscription_id,sampleLease)).data,sampleClaimed.data,'live test lease recovery remains possible during pause')
  assert.equal((await dataRequest(sampleWorker.rpc('finish_subscription_webhook',{...sampleFinish,succeeded:true,response_status:204,failure_reason:null}),{timeoutMs:5000})).data[0].state,'delivered','live test acknowledgements remain possible during pause')
  assert.equal((await pause(alice,false,samplePausedTeam.data[0].result.revision)).error,null)
  const exhaustedSample=await createWebhookTestForm(diagnosticClient).submit(editedAfterTest.subscription,true)
  assert.equal(exhaustedSample.outcome,'queued')
  for(let n=0;n<5;n++){
    const key=randomUUID(),r=await namedClaim(firstWorker,submittedSub.subscription_id,key)
    assert.equal(r.error,null);assert.equal(r.data[0].event_id,exhaustedSample.receipt.event_id)
    const ack=await dataRequest(firstWorker.rpc('finish_subscription_webhook',{team_id:'shop-a',subscription_id:submittedSub.subscription_id,delivery_id:r.data[0].event_id,claim_token:key,succeeded:false,response_status:503,failure_reason:'http_error'}),{timeoutMs:5000})
    assert.equal(ack.error,null);assert.equal(ack.data[0].state,n===4?'dead':'pending')
    await owner.query('UPDATE api.team_subscription_outbox SET available_at=clock_timestamp() WHERE event_id=$1',[r.data[0].event_id])
  }
  assert.equal((await createWebhookTestForm(diagnosticClient).submit(editedAfterTest.subscription,true)).outcome,'queued')
  const exhaustedRow=(await diagnostics.deliveries(submittedSub.subscription_id)).rows.find(r=>r.event_id===exhaustedSample.receipt.event_id)
  const sampleReplay=createReplayForm(diagnosticClient,submittedSub.subscription_id)
  const refusedSample=await sampleReplay.submit(exhaustedRow)
  assert.equal(refusedSample.uncertain,false);assert.equal(sampleReplay.pending,false);assert.match(refusedSample.message,/Another sample test/)
  {
  const {orderOperations,createOrderViewForm,createBulkFulfilmentForm}=await import(pathToFileURL(join(typed,'dist/order-operations.js')))
  const opsClient=teamsClient({url,accessToken:token},'shop-a'),operations=orderOperations(opsClient)
  const makeSubmitted=async(total=710001)=>{const id=randomUUID();assert.equal((await save(alice,{...args,request_key:randomUUID(),order_id:id,order_total:total})).error,null);const result=await action(alice,id,1,'submitted');assert.equal(result.error,null);return result.data[0]}
  const operationalOrders=await Promise.all([makeSubmitted(710001),makeSubmitted(710001),makeSubmitted(710002),makeSubmitted(710003)])
  const criteria={status:'submitted',minimum:710000,maximum:710010,sort:'total_desc'}
  const savedView=await createOrderViewForm(opsClient).submit({name:'Awaiting fulfilment',criteria,revision:null})
  assert.equal(savedView.outcome,'saved',JSON.stringify(savedView))
  let viewWrites=0,lostViewArgs
  const memberViewForm=createOrderViewForm(teamsClient({url,accessToken:memberToken,fetch:async(input,init)=>{const response=await fetch(input,init);if(String(input).includes('/save_order_view_once')){viewWrites++;lostViewArgs=JSON.parse(init.body);await response.text();throw TypeError('view response lost')}return response}},'shop-a'))
  assert.equal((await memberViewForm.submit({name:'Awaiting fulfilment',criteria,revision:null})).uncertain,true)
  await owner.query("DELETE FROM api.team_members WHERE tenant_id='shop-a' AND subject='ann'")
  const recoveredView=await memberViewForm.reconcile();assert.equal(recoveredView.outcome,'saved');assert.equal(viewWrites,1)
  await owner.query("INSERT INTO api.team_members(tenant_id,subject,role) VALUES('shop-a','ann','member')")
  assert.ok((await operations.views()).rows.some(r=>r.view_id===savedView.view.view_id));assert.ok((await operations.views()).rows.every(r=>r.subject==='alice'))
  assert.equal((await orderOperations(teamsClient({url,accessToken:memberToken},'shop-a')).views()).rows[0].subject,'ann')
  assert.deepEqual((await other.from('team_order_views').select().eq('tenant_id','shop-a')).data,[])
  assert.deepEqual((await amy.from('team_order_views').select()).data,[],'views are private even from other team admins')
  assert.deepEqual((await alice.from('team_order_views').update({name:'Forged'}).select().retry(false)).data,[])
  assert.equal((await alice.from('team_order_views').insert({...savedView.view,view_id:randomUUID(),name:'Direct'}).retry(false)).status,403)
  const viewArgs={team_id:'shop-a',request_id:randomUUID(),view_id:savedView.view.view_id,expected_revision:savedView.view.revision,view_name:'Awaiting fulfilment',status_filter:criteria.status,minimum_total:criteria.minimum,maximum_total:criteria.maximum,sort_order:criteria.sort}
  const saveViewRPC=(db,a)=>dataRequest(db.rpc('save_order_view_once',a),{timeoutMs:5000})
  const viewRace=await Promise.all([saveViewRPC(alice,viewArgs),saveViewRPC(alice,{...viewArgs,request_id:randomUUID(),sort_order:'total_asc'})])
  assert.deepEqual(viewRace.map(r=>r.status).sort(),[200,409])
  for(const sort of ['id_asc','id_desc','total_asc','total_desc']){
    let cursor,all=[]
    do{const page=await operations.orders({...criteria,sort},cursor,2);all.push(...page.rows);cursor=page.next??undefined}while(cursor)
    const expected=[...operationalOrders].sort((a,b)=>sort.startsWith('total_')?(a.total_cents-b.total_cents||a.id.localeCompare(b.id)):a.id.localeCompare(b.id));if(sort.endsWith('_desc'))expected.reverse()
    assert.deepEqual(all.map(r=>r.id),expected.map(r=>r.id),'keyset pagination must preserve total ties in '+sort)
  }
  assert.equal((await operations.orders({...criteria,minimum:710002,maximum:710003})).rows.length,2)
  const viewCSV=await exportOrders(opsClient,criteria),csvIDs=viewCSV.trimEnd().split('\r\n').slice(1).map(line=>line.split(',')[0].slice(1,-1))
  assert.deepEqual(csvIDs,[...operationalOrders].sort((a,b)=>b.total_cents-a.total_cents||b.id.localeCompare(a.id)).map(r=>r.id),'CSV export must honor the same saved view range and ordering')
  const selected=operationalOrders.slice(0,2).sort((a,b)=>a.id.localeCompare(b.id)),selection=selected.map(r=>({id:r.id,version:r.version}))
  const validPreview=await operations.preview(selection);assert.ok(validPreview.every(r=>r.reason==='eligible'))
  const foreignId=randomUUID();assert.equal((await save(other,{...args,team_id:'shop-b',request_key:randomUUID(),order_id:foreignId})).error,null)
  const foreignPreview=await operations.preview([{id:foreignId,version:1}]);assert.equal(foreignPreview[0].reason,'unavailable');assert.equal(foreignPreview[0].current,null)
  const previewRPC=(db,value)=>dataRequest(db.rpc('preview_team_order_fulfilment',{team_id:'shop-a',reviewed:value}),{timeoutMs:5000})
  const bulkRPC=(db,value,key=randomUUID())=>dataRequest(db.rpc('fulfil_team_orders_once',{team_id:'shop-a',operation_id:key,reviewed:value}),{timeoutMs:5000})
  for(const db of [member,other]){assert.equal((await previewRPC(db,selection)).status,403);assert.equal((await bulkRPC(db,selection)).status,403)}
  for(const invalid of [[],[{id:selected[0].id,version:0}],[...selection,selection[0]],Array.from({length:21},()=>({id:randomUUID(),version:1})),[{id:selected[0].id,version:2,hidden:true}]])assert.equal((await bulkRPC(alice,invalid)).status,400)
  assert.equal((await action(alice,selected[1].id,selected[1].version,'fulfilled')).error,null)
  const stalePreview=await operations.preview(selection);assert.equal(stalePreview.find(r=>r.id===selected[1].id).reason,'changed')
  const counts=async()=> (await owner.query("SELECT (SELECT count(*) FROM api.team_order_events)::int events,(SELECT count(*) FROM api.team_subscription_outbox)::int named,(SELECT count(*) FROM api.team_order_outbox)::int legacy,(SELECT count(*) FROM api.team_bulk_order_operations)::int batches")).rows[0]
  const beforeStale=await counts(),stale=await bulkRPC(alice,selection)
  assert.equal(stale.status,409);assert.equal(stale.error.message,'bulk_order_preview_stale');assert.deepEqual(await counts(),beforeStale,'stale later rows roll back earlier transitions, outbox and receipts')
  assert.equal((await owner.query('SELECT status FROM api.team_orders WHERE id=$1',[selected[0].id])).rows[0].status,'submitted')
  const remaining=[selected[0],...operationalOrders.slice(2)].map(r=>({id:r.id,version:r.version})),bulkKey=randomUUID()
  const beforeSuccess=await counts(),repeated=await Promise.all(Array.from({length:5},()=>bulkRPC(alice,remaining,bulkKey)))
  for(const result of repeated){assert.equal(result.error,null,JSON.stringify(result.error));assert.deepEqual(result.data,repeated[0].data)}
  const afterSuccess=await counts();assert.equal(afterSuccess.events-beforeSuccess.events,3);assert.equal(afterSuccess.named-beforeSuccess.named,3);assert.equal(afterSuccess.legacy,beforeSuccess.legacy);assert.equal(afterSuccess.batches-beforeSuccess.batches,1)
  assert.equal((await bulkRPC(alice,remaining.slice(0,1),bulkKey)).error.message,'bulk_order_payload_conflict')
  assert.deepEqual(repeated[0].data[0].result.map(r=>r.status),['fulfilled','fulfilled','fulfilled'])
  const recoveryOrders=await Promise.all([makeSubmitted(720001),makeSubmitted(720002)])
  const recoveryPreview=await operations.preview(recoveryOrders.map(r=>({id:r.id,version:r.version})))
  let bulkWrites=0,recoveredBulkArgs
  const lostBulkForm=createBulkFulfilmentForm(teamsClient({url,accessToken:token,fetch:async(input,init)=>{const response=await fetch(input,init);if(String(input).includes('/fulfil_team_orders_once')){bulkWrites++;recoveredBulkArgs=JSON.parse(init.body);await response.text();throw TypeError('bulk response lost')}return response}},'shop-a'))
  assert.equal((await lostBulkForm.submit(recoveryPreview,true)).uncertain,true)
  await owner.query("UPDATE api.team_members SET role='owner' WHERE tenant_id='shop-a' AND subject='amy'; UPDATE api.team_members SET role='member' WHERE tenant_id='shop-a' AND subject='alice'")
  const recoveredBulk=await lostBulkForm.reconcile();assert.equal(recoveredBulk.outcome,'fulfilled',JSON.stringify(recoveredBulk));assert.equal(bulkWrites,1)
  assert.equal((await bulkRPC(alice,recoveryOrders.map(r=>({id:r.id,version:r.version})))).status,403)
  assert.deepEqual((await other.rpc('get_team_bulk_order_receipt',{team_id:'shop-a',operation_key:recoveredBulkArgs.operation_id})).data,[])
  await owner.query("UPDATE api.team_members SET role='owner' WHERE tenant_id='shop-a' AND subject='alice'; UPDATE api.team_members SET role='admin' WHERE tenant_id='shop-a' AND subject='amy'")
  assert.ok((await orderOperations(teamsClient({url,accessToken:memberToken},'shop-a')).history()).rows.some(r=>r.operation_id===recoveredBulkArgs.operation_id))
  assert.deepEqual((await alice.from('team_bulk_order_operations').delete().select().retry(false)).data,[])
  const raceOrders=await Promise.all([makeSubmitted(730001),makeSubmitted(730002)]),raceSelection=raceOrders.map(r=>({id:r.id,version:r.version}))
  const races=await Promise.all([bulkRPC(alice,raceSelection),bulkRPC(amy,raceSelection)])
  assert.deepEqual(races.map(r=>r.status).sort(),[200,409],'overlapping distinct operations cannot transition twice')
  const unsentOrder=await makeSubmitted(735001),unsentPreview=await operations.preview([{id:unsentOrder.id,version:unsentOrder.version}])
  let unsentFirst=true
  const unsentForm=createBulkFulfilmentForm(teamsClient({url,accessToken:token,fetch:async(input,init)=>{if(unsentFirst&&String(input).includes('/fulfil_team_orders_once')){unsentFirst=false;throw TypeError('request never reached server')}return fetch(input,init)}},'shop-a'))
  assert.equal((await unsentForm.submit(unsentPreview,true)).uncertain,true)
  assert.equal((await action(alice,unsentOrder.id,unsentOrder.version,'fulfilled')).error,null)
  assert.equal((await unsentForm.reconcile()).uncertain,false,'receipt-checked stale refusal proves the original batch did not commit')
  assert.equal(unsentForm.pending,false)
  const waitingOrder=await makeSubmitted(740001)
  await owner.query('BEGIN')
  try{
    await owner.query("SELECT pg_advisory_xact_lock(hashtextextended('gregale:team-admin:shop-a',0))")
    const waitForLock=async count=>{for(let n=0;n<100;n++){const r=await owner.query("SELECT count(*)::int n FROM pg_locks l JOIN pg_stat_activity a ON a.pid=l.pid WHERE l.locktype='advisory' AND NOT l.granted AND a.datname=$1 AND a.usename=$2",[database,role]);if(r.rows[0].n>=count)return;await new Promise(r=>setTimeout(r,20))}assert.fail('bulk authorization request never reached the team lock')}
    const demote=change(alice,'amy',await currentRevision('amy'),'member');await waitForLock(1)
    const queued=bulkRPC(amy,[{id:waitingOrder.id,version:waitingOrder.version}]);await waitForLock(2)
    await owner.query('COMMIT');assert.equal((await demote).error,null);assert.equal((await queued).status,403,'writer rights must be checked again after a queued membership change')
  }finally{await owner.query('ROLLBACK')}
  await owner.query("UPDATE api.team_members SET role='admin' WHERE tenant_id='shop-a' AND subject='amy'")
  await owner.query('BEGIN ISOLATION LEVEL REPEATABLE READ')
  try{await owner.query(`SET LOCAL ROLE "${role}"`);await owner.query("SELECT set_config('request.jwt.claims',$1,true)",[JSON.stringify({sub:'alice'})]);await assert.rejects(owner.query("SELECT * FROM api.fulfil_team_orders_once('shop-a',$1,$2::jsonb)",[randomUUID(),JSON.stringify([{id:waitingOrder.id,version:waitingOrder.version}])]),/order_read_committed_required/)}finally{await owner.query('ROLLBACK')}

  }
  {
  const {sharedOrderViews,createSharedViewForm}=await import(pathToFileURL(join(typed,'dist/shared-views.js')))
  const {orderOperations,createBulkFulfilmentForm,createOrderViewForm}=await import(pathToFileURL(join(typed,'dist/order-operations.js')))
  const sharedClient=teamsClient({url,accessToken:token},'shop-a'),sharedAPI=sharedOrderViews(sharedClient)
  const memberClient=teamsClient({url,accessToken:memberToken},'shop-a')
  const criteria={status:'submitted',minimum:760000,maximum:760010,sort:'total_desc'}
  const published=await createSharedViewForm(sharedClient).save({name:'Team fulfilment queue',criteria,revision:null})
  assert.equal(published.outcome,'confirmed',JSON.stringify(published));assert.equal(published.receipt.shared.version,1)
  const original=published.receipt.shared
  assert.ok((await sharedOrderViews(memberClient).list()).rows.some(r=>r.view_id===original.view_id))
  assert.deepEqual((await other.from('team_shared_order_views').select().eq('tenant_id','shop-a')).data,[])
  const publishArgs={team_id:'shop-a',request_id:randomUUID(),view_id:original.view_id,expected_revision:original.revision,view_name:original.name,status_filter:original.status_filter,minimum_total:original.minimum_total,maximum_total:original.maximum_total,sort_order:original.sort_order}
  const publishRPC=(db,a)=>dataRequest(db.rpc('save_shared_order_view_once',a),{timeoutMs:5000})
  const copyRPC=(db,a)=>dataRequest(db.rpc('copy_shared_order_view_once',a),{timeoutMs:5000})
  assert.equal((await publishRPC(member,publishArgs)).status,403);assert.equal((await publishRPC(other,publishArgs)).status,403)
  assert.deepEqual((await alice.from('team_shared_order_views').update({name:'Direct'}).select().retry(false)).data,[])
  const raced=await Promise.all([publishRPC(alice,publishArgs),publishRPC(amy,{...publishArgs,request_id:randomUUID(),sort_order:'total_asc'})])
  assert.deepEqual(raced.map(r=>r.status).sort(),[200,409]);let current=(await sharedAPI.list()).rows.find(r=>r.view_id===original.view_id)
  assert.equal(current.version,2)
  const staleCopy={team_id:'shop-a',request_id:randomUUID(),shared_view_id:original.view_id,expected_revision:original.revision,personal_view_id:randomUUID(),personal_name:'Stale copy'}
  assert.equal((await copyRPC(member,staleCopy)).error.message,'shared_view_revision_conflict')
  let copyWrites=0,copyArgs
  const lostCopy=createSharedViewForm(teamsClient({url,accessToken:memberToken,fetch:async(input,init)=>{const response=await fetch(input,init);if(String(input).includes('/copy_shared_order_view_once')){copyWrites++;copyArgs=JSON.parse(init.body);await response.text();throw TypeError('copy response lost')}return response}},'shop-a'))
  assert.equal((await lostCopy.copy(current,'My private team copy')).uncertain,true)
  await owner.query("DELETE FROM api.team_members WHERE tenant_id='shop-a' AND subject='ann'")
  const copied=await lostCopy.reconcile();assert.equal(copied.outcome,'confirmed',JSON.stringify(copied));assert.equal(copyWrites,1)
  await owner.query("INSERT INTO api.team_members(tenant_id,subject,role) VALUES('shop-a','ann','member')")
  assert.equal(copied.receipt.personal.subject,'ann');assert.equal(copied.receipt.personal.sort_order,current.sort_order)
  const copies=await Promise.all(Array.from({length:4},()=>copyRPC(member,copyArgs)))
  for(const result of copies){assert.equal(result.error,null);assert.deepEqual(result.data,copies[0].data)}
  assert.equal((await owner.query("SELECT count(*)::int n FROM api.team_order_views WHERE subject='ann' AND name='My private team copy'")).rows[0].n,1)
  assert.deepEqual((await alice.from('team_shared_view_operations').select().eq('request_id',copyArgs.request_id)).data,[],'copy names and personal snapshots are private even from publishers')
  assert.deepEqual((await amy.from('team_order_views').select().eq('view_id',copied.receipt.personal.view_id)).data,[])
  assert.deepEqual((await alice.rpc('get_shared_order_view_receipt',{team_id:'shop-a',operation_key:copyArgs.request_id})).data,[])
  assert.equal((await copyRPC(member,{...copyArgs,personal_name:'Different'})).error.message,'shared_view_payload_conflict')
  const countsBefore=(await owner.query("SELECT (SELECT count(*) FROM api.team_order_views)::int views,(SELECT count(*) FROM api.team_shared_view_operations)::int operations")).rows[0]
  assert.equal((await copyRPC(member,{...copyArgs,request_id:randomUUID(),personal_view_id:randomUUID()})).status,409,'existing personal names cannot be overwritten by a new copy')
  assert.equal((await copyRPC(member,{...copyArgs,request_id:randomUUID(),personal_name:'Attempted overwrite'})).error.message,'order_view_revision_conflict','copy cannot replace an existing personal view ID')
  assert.deepEqual((await owner.query("SELECT (SELECT count(*) FROM api.team_order_views)::int views,(SELECT count(*) FROM api.team_shared_view_operations)::int operations")).rows[0],countsBefore,'failed copy rolls back personal writes and audit')
  const updated=await createSharedViewForm(teamsClient({url,accessToken:adminToken},'shop-a')).save({id:current.view_id,revision:current.revision,name:current.name,criteria:{...criteria,minimum:760002}})
  assert.equal(updated.outcome,'confirmed');assert.equal(updated.receipt.shared.version,3)
  assert.deepEqual((await copyRPC(member,copyArgs)).data,copies[0].data,'an idempotent copy keeps its original source revision after publication changes')
  const privateView=(await member.from('team_order_views').select().eq('view_id',copied.receipt.personal.view_id)).data[0]
  assert.equal(privateView.minimum_total,760000);assert.equal(updated.receipt.shared.minimum_total,760002)
  assert.equal((await sharedOrderViews(memberClient).history(original.view_id)).rows.filter(r=>r.action==='copy').length,0)
  assert.deepEqual((await sharedAPI.history(original.view_id)).rows.map(r=>r.shared.version).sort(),[1,2,3])
  const orders=[]
  for(const total of [760001,760002]){const id=randomUUID();assert.equal((await save(alice,{...args,request_key:randomUUID(),order_id:id,order_total:total})).error,null);assert.equal((await action(alice,id,1,'submitted')).error,null);orders.push(id)}
  const memberRows=await orderOperations(memberClient).orders({status:updated.receipt.shared.status_filter,minimum:updated.receipt.shared.minimum_total,maximum:updated.receipt.shared.maximum_total,sort:updated.receipt.shared.sort_order})
  assert.deepEqual(memberRows.rows.map(r=>r.id),[orders[1]])
  const writerOps=orderOperations(sharedClient),preview=await writerOps.preview(memberRows.rows.map(r=>({id:r.id,version:r.version})))
  assert.equal((await createBulkFulfilmentForm(memberClient).submit(preview,true)).uncertain,false,'applying a shared view does not grant writer access')
  assert.equal((await createBulkFulfilmentForm(sharedClient).submit(preview,true)).outcome,'fulfilled')
  let publishWrites=0
  const lostPublish=createSharedViewForm(teamsClient({url,accessToken:token,fetch:async(input,init)=>{const response=await fetch(input,init);if(String(input).includes('/save_shared_order_view_once')){publishWrites++;await response.text();throw TypeError('publication response lost')}return response}},'shop-a'))
  assert.equal((await lostPublish.save({name:'Recoverable publication',criteria,revision:null})).uncertain,true)
  await owner.query("UPDATE api.team_members SET role='owner' WHERE tenant_id='shop-a' AND subject='amy'; UPDATE api.team_members SET role='member' WHERE tenant_id='shop-a' AND subject='alice'")
  assert.equal((await lostPublish.reconcile()).outcome,'confirmed');assert.equal(publishWrites,1)
  await owner.query("UPDATE api.team_members SET role='owner' WHERE tenant_id='shop-a' AND subject='alice'; UPDATE api.team_members SET role='admin' WHERE tenant_id='shop-a' AND subject='amy'")
  assert.deepEqual((await alice.from('team_shared_view_operations').delete().select().retry(false)).data,[])
  const aliceCopy=await createSharedViewForm(sharedClient).copy(updated.receipt.shared,'Alice independent copy')
  assert.equal(aliceCopy.outcome,'confirmed')
  assert.equal((await publishRPC(alice,{...publishArgs,request_id:aliceCopy.receipt.request_id,expected_revision:updated.receipt.shared.revision})).error.message,'shared_view_payload_conflict')
  await owner.query('BEGIN')
  try{
    await owner.query("SELECT pg_advisory_xact_lock(hashtextextended('gregale:team-admin:shop-a',0))")
    const waitLock=async count=>{for(let n=0;n<100;n++){const r=await owner.query("SELECT count(*)::int n FROM pg_locks l JOIN pg_stat_activity a ON a.pid=l.pid WHERE l.locktype='advisory' AND NOT l.granted AND a.datname=$1 AND a.usename=$2",[database,role]);if(r.rows[0].n>=count)return;await new Promise(r=>setTimeout(r,20))}assert.fail('shared-view request did not reach membership lock')}
    const revoke=change(alice,'amy',await currentRevision('amy'),'member');await waitLock(1)
    const queued=publishRPC(amy,{...publishArgs,request_id:randomUUID(),expected_revision:updated.receipt.shared.revision});await waitLock(2)
    await owner.query('COMMIT');assert.equal((await revoke).error,null);assert.equal((await queued).status,403,'queued publication rechecks writer rights after revocation')
  }finally{await owner.query('ROLLBACK')}
  await owner.query("UPDATE api.team_members SET role='admin' WHERE tenant_id='shop-a' AND subject='amy'")
  }
  if(process.env.DATA_API_DEV_CLI) {
    const envelope=value=>JSON.stringify({version:1,fingerprint:fingerprint(value),snapshot:value})
    const baseline=join(typed,'baseline.json'), candidate=join(typed,'candidate.json'), declarations=join(typed,'database.types.ts')
    await writeFile(baseline,envelope(snapshot))
    await owner.query('ALTER TABLE api.team_orders ADD COLUMN label text')
    const expanded=await inspect(loginURL.toString(),['api'])
    await writeFile(candidate,envelope(expanded))
    const checkArgs=['data-api','check','--baseline',baseline,'--current',candidate,'--types',declarations]
    await assert.rejects(command(process.env.DATA_API_DEV_CLI,checkArgs,{cwd:typed,env:process.env}),/command_failed/)
    assert.equal(await readFile(declarations,'utf8'),generate(snapshot),'stale check must not overwrite declarations')
    await writeFile(declarations,generate(expanded))
    await assert.doesNotReject(command(process.env.DATA_API_DEV_CLI,checkArgs,{cwd:typed,env:process.env}),'fresh declarations must pass compatibility and type checks')
    await assert.doesNotReject(command(new URL('../../sdk/data/node_modules/.bin/tsc',import.meta.url).pathname,['--noEmit','--strict','--target','ES2022','--module','NodeNext','orders.ts'],{cwd:typed,env:process.env}),'explicitly projected client must compile after an additive nullable column')
    const compatible=teamsClient({url,accessToken:token},'shop-a')
    assert.ok((await compatible.list()).rows.length>0,'existing client must still read an additive contract')
  }
})
