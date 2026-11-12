import test from 'node:test'
import assert from 'node:assert/strict'
import net from 'node:net'
import http from 'node:http'
import { readFile, writeFile } from 'node:fs/promises'
import { spawn } from 'node:child_process'
import { once } from 'node:events'
import { createHash, randomUUID } from 'node:crypto'
import { createRequire } from 'node:module'
import { pathToFileURL } from 'node:url'
import { runDataJobs } from '../../cmd/gregale/templates/data-api-teams/workers/data-jobs.mjs'
import { pumpHooks } from '../../cmd/gregale/templates/data-api/hooks-worker.mjs'
const enabled = Boolean(process.env.DATA_API_TEST_DATABASE_URL && process.env.DATA_API_POSTGREST_BIN && process.env.DATA_API_RUNTIME_DIR)
const used = new Set()
async function port() { for (;;) { const s=net.createServer();await new Promise(r=>s.listen(0,'127.0.0.1',r));const p=s.address().port;await new Promise(r=>s.close(r));if(!used.has(p)){used.add(p);return p} } }

test('background jobs, immutable exports, behavior replays and scheduled hook draining through PostgreSQL/PostgREST', { skip: !enabled, timeout: 90000 }, async t => {
  const runtime=pathToFileURL(process.env.DATA_API_RUNTIME_DIR+'/'),require=createRequire(new URL('package.json',runtime)),pg=require('pg')
  const {runtimeConfig}=await import(new URL('config.mjs',runtime))
  const {createServer}=await import(new URL('server.mjs',runtime))
  const {inspect,fingerprint}=await import(new URL('types.mjs',runtime))
  const {inspectBehavior,compareBehavior,requireBehaviorReplays}=await import(new URL('behavior-contract.mjs',runtime))
  const {createDataClient}=await import('../../sdk/data/dist/index.js')
  const suffix=`${process.pid}_${Date.now()}`,database=`data_workflows_${suffix}`,role=`data_workflow_login_${suffix}`
  const admin=new pg.Client({connectionString:process.env.DATA_API_TEST_DATABASE_URL});await admin.connect()
  let owner,child,gateway,publisher
  t.after(async()=>{
    for(const server of [gateway,publisher])if(server){server.closeAllConnections();await new Promise(r=>server.close(r))}
    if(child&&child.exitCode===null){child.kill();await once(child,'exit')}
    await owner?.end();await admin.query(`DROP DATABASE IF EXISTS "${database}" WITH (FORCE)`);await admin.query(`DROP ROLE IF EXISTS "${role}"`);await admin.end()
  })
  await admin.query(`CREATE DATABASE "${database}"`);await admin.query(`CREATE ROLE "${role}" LOGIN NOINHERIT NOBYPASSRLS PASSWORD 'test-only'`)
  const ownerURL=new URL(process.env.DATA_API_TEST_DATABASE_URL);ownerURL.pathname=`/${database}`
  owner=new pg.Client({connectionString:ownerURL.toString()});await owner.connect()
  for(const migration of ['0001_team_orders.sql','0004_team_order_imports.sql','0013_background_data_jobs.sql'])await owner.query(await readFile(new URL(`../../cmd/gregale/templates/data-api-teams/migrations/sql/${migration}`,import.meta.url),'utf8'))
  for(const file of ['change-hooks.sql','change-hooks-status.sql'])await owner.query((await readFile(new URL(file,runtime),'utf8')).replaceAll('{{schema}}','api'))
  await owner.query(`INSERT INTO api.team_members VALUES ('a','alice','owner'),('a','amy','admin'),('b','bob','owner');
    CREATE TABLE api.precision_values(id bigint, amount numeric, payload jsonb); ALTER TABLE api.precision_values ENABLE ROW LEVEL SECURITY;
    CREATE POLICY reads ON api.precision_values FOR SELECT USING (true);
    INSERT INTO api.precision_values VALUES(9007199254740993,1.234567890123456789,'{"n":9007199254740993}');
    GRANT USAGE ON SCHEMA api TO "${role}"; GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA api TO "${role}";
    GRANT USAGE ON ALL SEQUENCES IN SCHEMA api TO "${role}"; GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA api TO "${role}";`)
  const login=new URL(ownerURL);login.username=role;login.password='test-only'
  const before=await inspectBehavior(ownerURL.toString(),['api'],role),shape=fingerprint(await inspect(login.toString(),['api']))
  await owner.query('ALTER POLICY own_memberships ON api.team_members USING (true)')
  const after=await inspectBehavior(ownerURL.toString(),['api'],role),changes=compareBehavior(before,after)
  assert.deepEqual(changes.map(c=>c.id),['policy:api.team_members.own_memberships'])
  assert.equal(fingerprint(await inspect(login.toString(),['api'])),shape,'policy semantics have a separate digest')
  assert.throws(()=>requireBehaviorReplays(changes,null,['team-access']))
  assert.throws(()=>requireBehaviorReplays(changes,{version:1,objects:{'*':['stale']}},['team-access']))
  requireBehaviorReplays(changes,{version:1,objects:{'policy:*':['team-access']}},['team-access'])
  await owner.query("ALTER POLICY own_memberships ON api.team_members USING (subject=current_setting('request.jwt.claims',true)::jsonb->>'sub')")
  await owner.query('CREATE SCHEMA private_helpers; CREATE FUNCTION private_helpers.probe() RETURNS integer LANGUAGE sql AS $$ SELECT 1 $$')
  const helpers=await inspectBehavior(ownerURL.toString(),['api'],role)
  await owner.query('CREATE OR REPLACE FUNCTION private_helpers.probe() RETURNS integer LANGUAGE sql AS $$ SELECT 2 $$')
  assert.deepEqual(compareBehavior(helpers,await inspectBehavior(ownerURL.toString(),['api'],role)).map(c=>c.id),['function:private_helpers.probe()'])
  const defaults=await inspectBehavior(ownerURL.toString(),['api'],role)
  await owner.query('ALTER TABLE api.team_orders ALTER COLUMN total_cents SET DEFAULT 0')
  assert.deepEqual(compareBehavior(defaults,await inspectBehavior(ownerURL.toString(),['api'],role)).map(c=>c.id),['default:api.team_orders.total_cents'])
  await assert.rejects(inspectBehavior(login.toString(),['api'],role),/schema owner/)

  let publishEnabled=false
  publisher=http.createServer((req,res)=>{
    if(req.url==='/identity')return res.end(JSON.stringify({token_type:'Bearer',access_token:'workload-test'}))
    let body='';req.on('data',b=>{body+=b});req.on('end',()=>{if(!publishEnabled){res.writeHead(503);return res.end()}res.end(JSON.stringify({results:JSON.parse(body).events.map((_,index)=>({index,status:'accepted'}))}))})
  })
  await new Promise(r=>publisher.listen(0,'127.0.0.1',r));const publishing=`http://127.0.0.1:${publisher.address().port}`
  const key='gdsk_'+'A'.repeat(43)
  const config=runtimeConfig({DATABASE_URL:login.toString(),DATA_API_ISSUER:'https://issuer.example',DATA_API_JWKS_URL:'https://issuer.example/jwks',DATA_API_AUDIENCE:'jobs',DATA_API_SERVER_KEY_A_SHA256:createHash('sha256').update(key).digest('hex')})
  config.snapshot=await inspect(login.toString(),['api']);config.functions=config.snapshot.functions
  config.changeEvents={schema:'api',endpoint:publishing+'/events',identity:publishing+'/identity'}
  const upstream=await port(),ready=await port()
  child=spawn(process.env.DATA_API_POSTGREST_BIN,[],{env:{...process.env,...config.postgrestEnv,PGRST_SERVER_PORT:String(upstream),PGRST_ADMIN_SERVER_PORT:String(ready)},stdio:['ignore','pipe','pipe']})
  let logs='';child.stdout.on('data',b=>{logs+=b});child.stderr.on('data',b=>{logs+=b})
  gateway=createServer(config,async token=>({sub:token,exp:Math.ceil(Date.now()/1000)+300}),upstream,ready,()=>{})
  await new Promise(r=>gateway.listen(0,'127.0.0.1',r));const url=`http://127.0.0.1:${gateway.address().port}`
  for(let i=0;i<100&&(await fetch(url+'/healthz')).status!==200&&child.exitCode===null;i++)await new Promise(r=>setTimeout(r,50))
  assert.equal((await fetch(url+'/healthz')).status,200,logs)
  const standard=await fetch(url+'/openapi-3.1.json',{headers:{authorization:'Bearer alice'}})
  assert.equal(standard.status,200)
  const specification=await standard.json()
  assert.equal(specification.openapi,'3.1.0')
  assert.ok(specification.components.schemas['api.team_order_jobs.Row'])
  assert.equal(specification.paths['/gregale_change_events'],undefined)
  if(process.env.DATA_API_OPENAPI_DOCUMENT_PATH)await writeFile(process.env.DATA_API_OPENAPI_DOCUMENT_PATH,JSON.stringify(specification))
  const db=subject=>createDataClient({url,accessToken:subject}).schema('api'),alice=db('alice'),amy=db('amy'),bob=db('bob'),backend=db(key)
  const precise=await createDataClient({url,accessToken:'alice',numericMode:'lossless'}).schema('api').from('precision_values').select()
  assert.deepEqual(precise.data,[{id:'9007199254740993',amount:'1.234567890123456789',payload:{n:'9007199254740993'}}])
  const call=async(client,name,args)=>{const response=await client.rpc(name,args);assert.equal(response.error,null,JSON.stringify(response.error));return response.data}
  const items=Array.from({length:25},()=>({id:randomUUID(),total_cents:123,status:'draft'})),importID=randomUUID()
  await call(alice,'start_team_order_import_once',{operation_id:importID,team_id:'a',source_label:'reviewed.csv',items})
  const id=randomUUID(),args={operation_id:id,team_id:'a',job_kind:'import',source_import:importID}
  await call(alice,'start_team_order_job',args);await call(alice,'start_team_order_job',args)
  assert.equal((await owner.query('SELECT count(*)::int AS n FROM api.team_order_jobs')).rows[0].n,1)
  assert.equal((await alice.rpc('claim_team_order_job')).status,403)
  assert.deepEqual((await bob.from('team_order_jobs').select()).data,[])
  const progress=[]
  assert.deepEqual(await runDataJobs({url,key,log:p=>progress.push(p)}),{completed:1,failed:0})
  assert.deepEqual(progress.map(p=>p.processed),[20,25])
  assert.equal((await alice.from('team_order_jobs').select().eq('id',id)).data[0].result.inserted,25)
  const exportID=randomUUID();await call(alice,'start_team_order_job',{operation_id:exportID,team_id:'a',job_kind:'export'})
  await runDataJobs({url,key,log:()=>{}})
  const saved=(await alice.from('team_order_jobs').select().eq('id',exportID)).data[0]
  assert.equal(saved.result.rows.length,25)
  await owner.query("INSERT INTO api.team_orders(id,tenant_id,created_by,total_cents) VALUES($1,'a','alice',999)",[randomUUID()])
  assert.deepEqual((await alice.from('team_order_jobs').select().eq('id',exportID)).data[0].result,saved.result,'export downloads keep the saved snapshot')
  const cancelled=randomUUID();await call(alice,'start_team_order_job',{operation_id:cancelled,team_id:'a',job_kind:'export'});await call(alice,'cancel_team_order_job',{job_id:cancelled})
  assert.deepEqual(await runDataJobs({url,key,log:()=>{}}),{completed:0,failed:0})
  const revokedImport=randomUUID(),revokedJob=randomUUID()
  await call(amy,'start_team_order_import_once',{operation_id:revokedImport,team_id:'a',source_label:'revoked.csv',items:[{id:randomUUID(),total_cents:1,status:'draft'}]})
  await call(amy,'start_team_order_job',{operation_id:revokedJob,team_id:'a',job_kind:'import',source_import:revokedImport})
  await owner.query("DELETE FROM api.team_members WHERE subject='amy'")
  assert.deepEqual(await runDataJobs({url,key,log:()=>{}}),{completed:0,failed:1})
  const failure=(await owner.query('SELECT failure FROM api.team_order_jobs WHERE id=$1',[revokedJob])).rows[0].failure
  assert.equal(failure.code,'42501');assert.equal(failure.first_row,1)
  const recovered=randomUUID();await call(alice,'start_team_order_job',{operation_id:recovered,team_id:'a',job_kind:'export'})
  const lease=(await call(backend,'claim_team_order_job',{}))[0]
  await owner.query("UPDATE api.team_order_jobs SET leased_until=clock_timestamp()-interval '1 second' WHERE id=$1",[recovered])
  await runDataJobs({url,key,log:()=>{}})
  const reclaimed=(await owner.query('SELECT * FROM api.team_order_jobs WHERE id=$1',[recovered])).rows[0]
  assert.equal(reclaimed.state,'completed');assert.notEqual(reclaimed.lease_token,lease.lease_token)

  await owner.query("SELECT gregale_hooks.enable('api.team_orders','keys')")
  await owner.query("INSERT INTO api.team_orders(id,tenant_id,created_by,total_cents) VALUES($1,'a','alice',1)",[randomUUID()])
  const ownerStatus=await fetch(url+'/__gregale/hooks/status',{headers:{authorization:`Bearer ${key}`}})
  assert.equal(ownerStatus.status,200);assert.equal((await ownerStatus.json()).pending,1)
  assert.equal((await fetch(url+'/__gregale/hooks/status',{headers:{authorization:'Bearer alice'}})).status,403)
  publishEnabled=true
  const status=await pumpHooks({url,key,log:()=>{}})
  assert.equal(status.pending,0,'scheduled worker drains external SQL writes without an application REST request')
})
