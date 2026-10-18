import test from 'node:test'
import assert from 'node:assert/strict'
import { randomBytes,randomUUID } from 'node:crypto'
import { createRequire } from 'node:module'
import { pathToFileURL,fileURLToPath } from 'node:url'
import { execFile,spawn } from 'node:child_process'
import { promisify } from 'node:util'
import { once } from 'node:events'
import net from 'node:net'

const enabled=Boolean(process.env.DATA_API_TEST_DATABASE_URL && process.env.DATA_API_RUNTIME_DIR && process.env.DATA_API_POSTGREST_BIN)
async function until(check){const expires=Date.now()+8000;while(!await check()){if(Date.now()>expires)assert.fail('Timed out waiting for rotation fixture');await new Promise(r=>setTimeout(r,20))}}
async function port(){const s=net.createServer();await new Promise(r=>s.listen(0,'127.0.0.1',r));const p=s.address().port;await new Promise(r=>s.close(r));return p}

test('binding refresh gates span rotating logins, pause races, failed preparation and per-login schema verification', {skip:!enabled,timeout:60000},async t=>{
  const root=pathToFileURL(process.env.DATA_API_RUNTIME_DIR+'/'),require=createRequire(new URL('package.json',root)),pg=require('pg')
  const {installRefreshBarrier,changeRefreshBarrier,checkRefreshSetup,refreshStatusReader}=await import(new URL('refresh.mjs',root))
  const {manageRefreshRotation,preflightRefreshRotation}=await import(new URL('refresh-rotation.mjs',root))
  const {installSQLDeadline}=await import(new URL('sql-deadline.mjs',root))
  const {inspect,fingerprint}=await import(new URL('types.mjs',root))
  const {runtimeConfig}=await import(new URL('config.mjs',root)),{createServer}=await import(new URL('server.mjs',root))
  const suffix=randomBytes(16).toString('hex'),database=`refresh_rotation_${suffix}`,ownerRole=`gregale_schema_${suffix}`,migrationRole=`refresh_migration_${suffix}`
  const scope=randomBytes(20).toString('hex'),binding=randomUUID(),roles=[`gregale_api_${randomBytes(20).toString('hex')}`],role=roles[0]
  const admin=new pg.Client({connectionString:process.env.DATA_API_TEST_DATABASE_URL});await admin.connect()
  const url=new URL(process.env.DATA_API_TEST_DATABASE_URL);url.pathname=`/${database}`
  let owner,control,held;const running=[]
  t.after(async()=>{
    for(const runtime of running){runtime.server.closeAllConnections();await new Promise(r=>runtime.server.close(r));if(runtime.engine.exitCode===null&&runtime.engine.signalCode===null){runtime.engine.kill();await once(runtime.engine,'exit')}await runtime.reader.close()}
    await held?.end();await owner?.end();await control?.end()
    await admin.query(`DROP DATABASE IF EXISTS "${database}" WITH (FORCE)`)
    for(const name of [...roles,migrationRole,ownerRole])await admin.query(`DROP ROLE IF EXISTS "${name}"`)
    await admin.end()
  })
  await admin.query(`CREATE ROLE "${ownerRole}" NOLOGIN NOINHERIT;COMMENT ON ROLE "${ownerRole}" IS 'gregale:schema-owner:v1';
    CREATE ROLE "${migrationRole}" LOGIN NOINHERIT NOCREATEROLE NOCREATEDB NOBYPASSRLS PASSWORD 'test-only';
    GRANT "${ownerRole}" TO "${migrationRole}";ALTER ROLE "${migrationRole}" SET role TO "${ownerRole}"`)
  await admin.query(`CREATE DATABASE "${database}"`)
  const bootstrap=new pg.Client({connectionString:url.toString()});await bootstrap.connect()
  async function createRole(client,name){await client.query(`CREATE ROLE "${name}" LOGIN NOINHERIT NOCREATEROLE NOCREATEDB NOBYPASSRLS PASSWORD 'test-only';
    COMMENT ON ROLE "${name}" IS 'gregale:credential:v1:${scope}:data_api';
    ALTER ROLE "${name}" SET statement_timeout=15000;ALTER ROLE "${name}" SET lock_timeout=5000;
    GRANT USAGE ON SCHEMA api TO "${name}";GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA api TO "${name}"`)}
  await bootstrap.query(`GRANT CREATE ON DATABASE "${database}" TO "${ownerRole}";REVOKE ALL ON DATABASE "${database}" FROM PUBLIC;GRANT CONNECT ON DATABASE "${database}" TO PUBLIC;ALTER SCHEMA public OWNER TO "${ownerRole}";
    REVOKE ALL ON SCHEMA public FROM PUBLIC;CREATE SCHEMA api AUTHORIZATION "${ownerRole}";SET ROLE "${ownerRole}";
    ALTER DEFAULT PRIVILEGES REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC;
    CREATE TABLE api.notes(id integer PRIMARY KEY,subject text NOT NULL);INSERT INTO api.notes VALUES(1,'alice'),(2,'bob');
    ALTER TABLE api.notes ENABLE ROW LEVEL SECURITY;CREATE POLICY owned ON api.notes USING(subject=current_setting('request.jwt.claims',true)::jsonb->>'sub');
    CREATE FUNCTION api.note_count() RETURNS bigint LANGUAGE sql SECURITY INVOKER AS 'SELECT count(*) FROM api.notes';
    COMMENT ON FUNCTION api.note_count() IS '@gregale:rpc';REVOKE ALL ON FUNCTION api.note_count() FROM PUBLIC;
    CREATE FUNCTION api.not_approved() RETURNS integer LANGUAGE sql SECURITY INVOKER AS 'SELECT 1';REVOKE ALL ON FUNCTION api.not_approved() FROM PUBLIC;
    RESET ROLE`)
  await createRole(bootstrap,role)
  await bootstrap.query(`SET ROLE "${ownerRole}";GRANT EXECUTE ON FUNCTION api.note_count() TO "${role}"`)
  await installRefreshBarrier(bootstrap,role)
  await bootstrap.query(`SET ROLE "${ownerRole}"`)
  await installSQLDeadline(bootstrap,role,{coordinatedRefresh:true})
  await bootstrap.end()
  const migrationURL=new URL(url);migrationURL.username=migrationRole;migrationURL.password='test-only'
  owner=new pg.Client({connectionString:migrationURL.toString()});await owner.connect()
  control=new pg.Client({connectionString:migrationURL.toString()});await control.connect()
  const sourceURL=new URL(url);sourceURL.username=role;sourceURL.password='test-only'
  const original=fingerprint(await inspect(sourceURL.toString(),['api']))
  const unenrolled=await preflightRefreshRotation(owner,role,binding,{sqlDeadline:true})
  assert.equal(unenrolled.ready,false)
  assert.equal(unenrolled.checks.find(c=>c.code==='enrollment_tables').status,'blocked')
  held=new pg.Client({connectionString:sourceURL.toString()});await held.connect();await held.query('BEGIN')
  await held.query("SELECT set_config('request.jwt.claims',$1,true)",[JSON.stringify({sub:'alice',gregale_serving_fingerprint:original})])
  await held.query('SELECT gregale_data_api.check_request()')
  const enrolling=manageRefreshRotation(owner,'enroll',role,binding,{sqlDeadline:true})
  await until(async()=>Number((await admin.query("SELECT count(*) FROM pg_stat_activity WHERE datname=$1 AND wait_event_type='Lock'",[database])).rows[0].count)>0)
  const queued=new pg.Client({connectionString:sourceURL.toString(),query_timeout:15000});await queued.connect()
  await queued.query("SELECT set_config('request.jwt.claims',$1,false)",[JSON.stringify({sub:'alice',gregale_serving_fingerprint:original})])
  const waiting=assert.rejects(queued.query('SELECT gregale_data_api.check_request()'),e=>e.code==='PT503')
  await until(async()=>Number((await admin.query("SELECT count(*) FROM pg_stat_activity WHERE datname=$1 AND wait_event_type='Lock'",[database])).rows[0].count)>1)
  await held.query('COMMIT');await held.end();held=null
  const approved=await enrolling
  await waiting;await queued.end()
  assert.equal(approved.fingerprint,original)
  assert.equal((await manageRefreshRotation(owner,'enroll',role,binding,{sqlDeadline:true})).enrolled,true)
  assert.equal((await owner.query('SELECT * FROM gregale_data_api.refresh_gate WHERE role_name=$1',[role])).rowCount,0,'legacy admissions cannot bypass the shared gate')
  const cliEnv={...process.env,MIGRATION_DATABASE_URL:migrationURL.toString(),DATA_API_SQL_DEADLINE:'1'}
  await t.test('owner preflight is read-only and reports actionable blockers without adopting drift',async()=>{
    const state=async()=>({
      bindings:(await owner.query('SELECT * FROM gregale_data_api.refresh_rotation_bindings ORDER BY binding_id,scope')).rows,
      members:(await owner.query('SELECT * FROM gregale_data_api.refresh_rotation_members ORDER BY role_name')).rows,
      hooks:(await owner.query("SELECT p.oid,p.prosrc,p.proacl,p.proconfig FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='gregale_data_api' ORDER BY p.oid")).rows,
      roles:(await admin.query('SELECT oid,rolname,rolcanlogin,rolinherit,rolconfig FROM pg_roles WHERE rolname=$1',[role])).rows,
    })
    const baseline=await state()
    const report=await preflightRefreshRotation(owner,role,binding,{sqlDeadline:true})
    assert.equal(report.ready,true,JSON.stringify(report))
    assert.equal(report.complete,true);assert.equal(report.member_count,1);assert.equal(report.paused,false)
    assert.ok(report.checks.every(c=>c.status==='passed' && c.next_action.length>0))
    assert.deepEqual(await state(),baseline)
    const run=async(expected,options={sqlDeadline:true},id=binding)=>{
      const before=await state(),report=await preflightRefreshRotation(owner,role,id,options)
      assert.equal(report.ready,false,JSON.stringify(report))
      assert.equal(report.checks.find(c=>c.code===expected).status,'blocked',JSON.stringify(report))
      assert.deepEqual(await state(),before,'preflight cannot repair or adopt the failed state')
      assert.equal((await owner.query('SHOW transaction_read_only')).rows[0].transaction_read_only,'off','preflight must end its transaction')
      const serialized=JSON.stringify(report)
      for(const secret of [migrationURL.toString(),role,ownerRole,scope,'test-only','prosrc'])assert.ok(!serialized.includes(secret),secret)
      return report
    }
    await run('binding_enrollment',{sqlDeadline:true},randomUUID())
    await run('sql_deadline',{sqlDeadline:false})
    await owner.query(`GRANT SELECT ON gregale_data_api.refresh_rotation_members TO "${role}"`)
    await run('enrollment_tables')
    await owner.query(`REVOKE SELECT ON gregale_data_api.refresh_rotation_members FROM "${role}"`)
    const definition=(await owner.query("SELECT pg_get_functiondef('gregale_data_api.refresh_status(text)'::regprocedure) AS body")).rows[0].body
    await owner.query("ALTER FUNCTION gregale_data_api.refresh_status(text) SET search_path=public")
    await run('refresh_hooks')
    await owner.query(definition)
    await admin.query(`ALTER ROLE "${role}" INHERIT`)
    await run('source_role')
    await admin.query(`ALTER ROLE "${role}" NOINHERIT`)
    await owner.query(`REVOKE EXECUTE ON FUNCTION gregale_data_api.refresh_status(text) FROM "${role}"`)
    await run('serving_grants')
    await owner.query(`GRANT EXECUTE ON FUNCTION gregale_data_api.refresh_status(text) TO "${role}"`)
    await owner.query(`REVOKE EXECUTE ON FUNCTION api.note_count() FROM "${role}"`)
    await run('rpc_grants')
    await owner.query(`GRANT EXECUTE ON FUNCTION api.note_count() TO "${role}"`)
    await owner.query('ALTER TABLE api.notes ADD COLUMN preflight_drift text')
    const drift=await run('catalog_witness')
    assert.equal(drift.checks.find(c=>c.code==='serving_contract').status,'blocked')
    await owner.query('ALTER TABLE api.notes DROP COLUMN preflight_drift')
    // Dropping the column still leaves catalog evidence. Reapprove explicitly.
    const token='c'.repeat(64)
    const approve=async()=>{await changeRefreshBarrier(owner,'pause',role,token);await changeRefreshBarrier(owner,'resume',role,token,fingerprint(await inspect(sourceURL.toString(),['api'])))}
    await approve()
    await owner.query(`ALTER POLICY owned ON api.notes TO "${role}"`)
    await approve()
    const rls=await run('rls_compatibility')
    assert.equal(rls.checks.find(c=>c.code==='catalog_witness').status,'passed','approved login-specific RLS still cannot transfer to a new login')
    await owner.query('ALTER POLICY owned ON api.notes TO PUBLIC')
    await approve()
    const good=await promisify(execFile)(process.execPath,[fileURLToPath(new URL('refresh-rotation.mjs',root)),'preflight',role,binding],{env:cliEnv,timeout:15000})
    assert.equal(JSON.parse(good.stdout).ready,true)
    await assert.rejects(promisify(execFile)(process.execPath,[fileURLToPath(new URL('refresh-rotation.mjs',root)),'preflight',role,randomUUID()],{env:cliEnv,timeout:15000}),error=>{
      assert.equal(error.code,1);assert.equal(error.stderr,'');assert.equal(JSON.parse(error.stdout).ready,false);return true
    })
    await changeRefreshBarrier(owner,'pause',role,token)
    const paused=await preflightRefreshRotation(owner,role,binding,{sqlDeadline:true})
    assert.equal(paused.ready,true,'an unchanged paused gate may prepare a replacement without a refresh token')
    assert.equal(paused.paused,true)
    await changeRefreshBarrier(owner,'resume',role,token,original)
  })
  const before=(await owner.query('SELECT * FROM gregale_data_api.refresh_rotation_members')).rows
  const cli=await promisify(execFile)(process.execPath,[fileURLToPath(new URL('refresh-rotation.mjs',root)),'check-enrollment',role,binding],{env:cliEnv,timeout:15000})
  assert.equal(JSON.parse(cli.stdout).enrolled,true)
  assert.deepEqual((await owner.query('SELECT * FROM gregale_data_api.refresh_rotation_members')).rows,before)
  await assert.rejects(installRefreshBarrier(owner,role),/Refresh rotation/)

  async function prepare(name){
    roles.push(name);const client=new pg.Client({connectionString:url.toString()});await client.connect();await client.query('BEGIN')
    try{await createRole(client,name);await client.query(`SET LOCAL ROLE "${ownerRole}"`);await client.query('SELECT gregale_data_api.prepare_refresh_member($1::uuid,$2,$3::name)',[binding,scope,name]);await client.query('COMMIT')}
    catch(error){await client.query('ROLLBACK');throw error}finally{await client.end()}
  }
  async function start(name){
    const connection=new URL(url);connection.username=name;connection.password='test-only'
    const config=runtimeConfig({DATABASE_URL:connection.toString(),DATA_API_ISSUER:'https://issuer.example',DATA_API_JWKS_URL:'https://issuer.example/jwks',DATA_API_AUDIENCE:'notes',DATA_API_COORDINATED_REFRESH:'1',DATA_API_SQL_DEADLINE:'1'})
    config.snapshot=await inspect(config.connection,config.schemas);config.functions=config.snapshot.functions;config.fingerprint=fingerprint(config.snapshot)
    const reader=refreshStatusReader(config.connection);config.refreshStatus=reader.read
    const upstream=await port(),ready=await port(),engine=spawn(process.env.DATA_API_POSTGREST_BIN,[],{env:{...process.env,...config.postgrestEnv,PGRST_SERVER_PORT:String(upstream),PGRST_ADMIN_SERVER_PORT:String(ready)},stdio:'ignore'})
    await until(async()=>{try{return(await fetch(`http://127.0.0.1:${ready}/ready`)).status===200}catch{return false}})
    const server=createServer(config,async sub=>({sub,exp:9999999999}),upstream,ready,()=>{});await new Promise(r=>server.listen(0,'127.0.0.1',r))
    const result={config,reader,server,engine,url:`http://127.0.0.1:${server.address().port}`};running.push(result);return result
  }
  async function request(runtime,sub='alice',token,path='/rest/v1/notes?select=id'){
    const response=await fetch(runtime.url+path,{headers:{Authorization:`Bearer ${sub}`,...(token?{'X-Gregale-Refresh-Token':token}:{})}})
    return {status:response.status,body:await response.json()}
  }
  const old=await start(role),token='a'.repeat(64)
  assert.deepEqual((await request(old)).body,[{id:1}])
  held=new pg.Client({connectionString:sourceURL.toString()});await held.connect();await held.query('BEGIN')
  await held.query("SELECT set_config('request.jwt.claims',$1,true)",[JSON.stringify({sub:'alice',gregale_serving_fingerprint:original})])
  await held.query('SELECT gregale_data_api.check_request()')
  let paused=false
  const pausing=changeRefreshBarrier(control,'pause',role,token).then(()=>{paused=true})
  await until(async()=>Number((await admin.query("SELECT count(*) FROM pg_stat_activity WHERE datname=$1 AND wait_event_type='Lock'",[database])).rows[0].count)>0)
  const nextRole=`gregale_api_${randomBytes(20).toString('hex')}`
  const preparing=prepare(nextRole)
  assert.equal(paused,false)
  await held.query('COMMIT');await held.end();held=null
  await pausing;await preparing
  const next=await start(nextRole)
  assert.equal(next.config.fingerprint,original,'prepared login must reproduce the approved normalized contract')
  assert.equal((await request(old)).status,503);assert.equal((await request(next)).status,503)
  assert.deepEqual((await request(next,'bob',token)).body,[{id:2}])
  await assert.rejects(changeRefreshBarrier(owner,'resume',nextRole,'b'.repeat(64),original),/owner changed/)
  const directURL=new URL(url);directURL.username=nextRole;directURL.password='test-only'
  const restricted=new pg.Client({connectionString:directURL.toString()});await restricted.connect()
  try{
    await assert.rejects(restricted.query('SELECT * FROM gregale_data_api.refresh_rotation_bindings'),e=>e.code==='42501')
    await assert.rejects(restricted.query('SELECT gregale_data_api.refresh_catalog_witness(current_user::name)'),e=>e.code==='42501')
    await assert.rejects(restricted.query('SELECT api.not_approved()'),e=>e.code==='42501')
  }finally{await restricted.end()}
  await owner.query('ALTER TABLE api.notes ADD COLUMN extra text')
  const failedRole=`gregale_api_${randomBytes(20).toString('hex')}`
  await assert.rejects(prepare(failedRole),e=>e.code==='55000')
  assert.equal((await admin.query('SELECT 1 FROM pg_roles WHERE rolname=$1',[failedRole])).rowCount,0,'failed preparation rolls back the candidate')
  assert.equal((await next.reader.read()).paused,true)
  await owner.query(`REVOKE EXECUTE ON FUNCTION api.note_count() FROM "${role}"`)
  const newContract=fingerprint(await inspect(directURL.toString(),['api']))
  await assert.rejects(changeRefreshBarrier(owner,'resume',nextRole,token,original),/Schema changed/)
  await changeRefreshBarrier(owner,'resume',nextRole,token,newContract)
  const members=(await owner.query('SELECT role_name,fingerprint FROM gregale_data_api.refresh_rotation_members ORDER BY role_name')).rows
  assert.notEqual(members[0].fingerprint,members[1].fingerprint,'each login keeps its verified permissions contract')
  assert.equal((await request(old)).status,409);assert.equal((await request(next)).status,409)
  assert.equal((await checkRefreshSetup(owner,nextRole)).live_fingerprint,newContract)
  await prepare(failedRole)
  const repairedURL=new URL(url);repairedURL.username=failedRole;repairedURL.password='test-only'
  assert.equal(fingerprint(await inspect(repairedURL.toString(),['api'])),newContract,'owner verification permits the same candidate to resume')
  const fresh=await start(failedRole)
  assert.deepEqual((await request(fresh,'alice')).body,[{id:1}])
  assert.deepEqual((await request(fresh,'bob')).body,[{id:2}])
})
