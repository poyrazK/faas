import test from 'node:test'
import assert from 'node:assert/strict'
import {createRequire} from 'node:module'
import {pathToFileURL} from 'node:url'
import {join} from 'node:path'
import {readFile} from 'node:fs/promises'
import {randomBytes} from 'node:crypto'
import {spawn} from 'node:child_process'
import {once} from 'node:events'
import net from 'node:net'
const runtime=process.env.DATA_API_RUNTIME_DIR,consoleDirectory=process.env.DATA_API_SUPPORT_CONSOLE_DIR
const enabled=runtime&&consoleDirectory&&process.env.DATA_API_TEST_DATABASE_URL&&process.env.DATA_API_POSTGREST_BIN
const port=async()=>{const server=net.createServer();await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));const result=server.address().port;await new Promise(resolve=>server.close(resolve));return result}
test('customer support console reads real PostgreSQL views with column grants and tenant RLS',{skip:!enabled,timeout:30000},async t=>{
 const require=createRequire(join(runtime,'package.json')),pg=require('pg'),{runtimeConfig}=await import(pathToFileURL(join(runtime,'config.mjs'))),{createServer}=await import(pathToFileURL(join(runtime,'server.mjs'))),{inspect,fingerprint}=await import(pathToFileURL(join(runtime,'types.mjs')))
 const {demoProvider,listen}=await import(pathToFileURL(join(consoleDirectory,'demo.mjs'))),{settings}=await import(pathToFileURL(join(consoleDirectory,'auth.mjs'))),{createConsole}=await import(pathToFileURL(join(consoleDirectory,'server.mjs')))
 const suffix=randomBytes(6).toString('hex'),database='support_console_'+suffix,role='support_reader_'+suffix,password=randomBytes(24).toString('hex'),admin=new pg.Client({connectionString:process.env.DATA_API_TEST_DATABASE_URL});await admin.connect()
 let owner,child,gateway,consoleServer,provider
 t.after(async()=>{
  for(const server of [consoleServer,gateway,provider?.server])if(server){server.closeAllConnections();await new Promise(resolve=>server.close(resolve))}
  if(child&&child.exitCode===null){child.kill('SIGTERM');await once(child,'exit')}
  await owner?.end().catch(()=>{})
  await admin.query('DROP DATABASE IF EXISTS "'+database+'" WITH (FORCE)')
  await admin.query('DROP ROLE IF EXISTS "'+role+'"');await admin.end()
 })
 await admin.query('CREATE ROLE "'+role+'" LOGIN NOSUPERUSER NOINHERIT NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD $1'.replace('$1',"'"+password+"'"))
 await admin.query('CREATE DATABASE "'+database+'"')
 const ownerURL=new URL(process.env.DATA_API_TEST_DATABASE_URL);ownerURL.pathname='/'+database;owner=new pg.Client({connectionString:ownerURL.toString()});await owner.connect()
 await owner.query('REVOKE ALL ON DATABASE "'+database+'" FROM PUBLIC; GRANT CONNECT ON DATABASE "'+database+'" TO "'+role+'"; CREATE SCHEMA api; GRANT USAGE ON SCHEMA api TO "'+role+'";')
 const sql=await readFile(new URL('../../cmd/gregale/templates/data-api-starter/migrations/examples/support/support.sql',import.meta.url),'utf8');await owner.query(sql.replaceAll('gregale_data_api','"'+role+'"'))
 await owner.query("INSERT INTO support_private.cases(tenant,title,status,internal_notes) VALUES ('shop-a','Case A','open','private A'),('shop-b','Case B','closed','private B'); INSERT INTO support_private.replies(tenant,case_id,body,internal_notes) SELECT tenant,id,'Reply '||tenant,'private reply' FROM support_private.cases;")
 await owner.query("CREATE OR REPLACE VIEW api.support_cases WITH (security_invoker=true,security_barrier=true) AS SELECT id,tenant,title,status,'2026-01-01T00:00:00Z'::timestamptz AS occurred_at FROM support_private.cases; CREATE OR REPLACE VIEW api.support_replies WITH (security_invoker=true,security_barrier=true) AS SELECT id,tenant,case_id,body,'2026-01-02T00:00:00Z'::timestamptz AS occurred_at FROM support_private.replies;")
 const grants=(await owner.query("SELECT has_column_privilege($1,'support_private.cases','internal_notes','SELECT') AS secret,has_table_privilege($1,'support_private.cases','UPDATE') AS writable",[role])).rows[0];assert.deepEqual(grants,{secret:false,writable:false})
 const loginURL=new URL(ownerURL);loginURL.username=role;loginURL.password=password
 const config=runtimeConfig({DATABASE_URL:loginURL.toString(),DATA_API_ISSUER:'https://issuer.example',DATA_API_JWKS_URL:'https://issuer.example/jwks',DATA_API_AUDIENCE:'support-data-demo'});config.snapshot=await inspect(loginURL.toString(),['api']);config.fingerprint=fingerprint(config.snapshot);config.functions=[]
 const upstreamPort=await port(),adminPort=await port()
 child=spawn(process.env.DATA_API_POSTGREST_BIN,[],{env:{...process.env,...config.postgrestEnv,PGRST_SERVER_PORT:String(upstreamPort),PGRST_ADMIN_SERVER_PORT:String(adminPort)},stdio:'ignore'})
 provider=await demoProvider();gateway=createServer(config,provider.verify,upstreamPort,adminPort,()=>{});await listen(gateway)
 const api='http://127.0.0.1:'+gateway.address().port
 for(let count=0;count<100;count++){if((await fetch(api+'/healthz')).ok)break;await new Promise(resolve=>setTimeout(resolve,50))}
 assert.equal((await fetch(api+'/healthz')).status,200)
 const consolePort=await port(),origin='http://127.0.0.1:'+consolePort,consoleConfig=settings({SUPPORT_ORIGIN:origin,SUPPORT_OIDC_ISSUER:provider.issuer,SUPPORT_OIDC_CLIENT_ID:'support-console-demo',SUPPORT_DATA_AUDIENCE:'support-data-demo',SUPPORT_DATA_API_URL:api,SUPPORT_SCHEMA_FINGERPRINT:config.fingerprint,SUPPORT_SESSION_KEY:randomBytes(32).toString('base64url')}),definition=JSON.parse(await readFile(join(consoleDirectory,'support-console.json'),'utf8'))
 for(const r of definition.resources)r.columns.push("occurred_at")
 definition.timelines=[{id:"history",label:"Customer history",resource:"cases",from:["tenant","id"],sources:[{resource:"cases",to:["tenant","id"],timestamp:"occurred_at",title:"title"},{resource:"replies",to:["tenant","case_id"],timestamp:"occurred_at",title:"body"}]}]
 definition.customer={resource:"cases",title:"title",lookup:["title","id"],relationships:["replies"],timeline:"history"}
 const auditEvents=[]
 consoleServer=await createConsole(consoleConfig,definition,{auditSink:event=>auditEvents.push(event)});await listen(consoleServer,consolePort)
 const signIn=async agent=>{const start=await fetch(origin+'/auth/login',{redirect:'manual'}),cookie=start.headers.getSetCookie()[0].split(';')[0],authorize=new URL(start.headers.get('location'));authorize.searchParams.set('agent',agent);const approved=await fetch(authorize,{redirect:'manual'}),callback=await fetch(approved.headers.get('location'),{redirect:'manual',headers:{Cookie:cookie}});assert.equal(callback.status,303);return callback.headers.getSetCookie().find(value=>value.startsWith('support_session=')).split(';')[0]}
 for(const agent of ['a','b']){
  const cookie=await signIn(agent),response=await fetch(origin+'/api/data?resource=cases',{headers:{Cookie:cookie}});assert.equal(response.status,200);const rows=await response.json();assert.equal(rows.length,1);assert.equal(rows[0].tenant,'shop-'+agent);assert.ok(!JSON.stringify(rows).includes('private'))
  const scope=encodeURIComponent(JSON.stringify({source:'cases',relationship:'replies',values:[rows[0].tenant,rows[0].id]})),replies=await(await fetch(origin+'/api/data?resource=replies&scope='+scope,{headers:{Cookie:cookie}})).json();assert.equal(replies.length,1);assert.equal(replies[0].tenant,'shop-'+agent)
  const timelineParams=new URLSearchParams({timeline:'history',values:JSON.stringify([rows[0].tenant,rows[0].id])}),timeline=await fetch(origin+'/api/timeline?'+timelineParams,{headers:{Cookie:cookie}});assert.equal(timeline.status,200);const events=(await timeline.json()).events;assert.equal(events.length,2);assert.equal(events[0].resource,'replies');assert.ok(events.every(e=>e.row.tenant==='shop-'+agent));assert.ok(!JSON.stringify(events).includes('private'))
  const customer=await fetch(origin+'/api/customer?'+new URLSearchParams({keys:JSON.stringify([rows[0].tenant,rows[0].id])}),{headers:{Cookie:cookie}});assert.equal(customer.status,200);const profile=await customer.json();assert.equal(profile.row.tenant,'shop-'+agent);assert.equal(profile.related[0].rows.length,1);assert.ok(!JSON.stringify(profile).includes('private'));assert.equal((await fetch(origin+'/api/customer?'+new URLSearchParams({keys:JSON.stringify(['shop-foreign',rows[0].id])}),{headers:{Cookie:cookie}})).status,404)
  assert.ok(auditEvents.some(e=>e.action==='customer.profile'&&e.tenant==='shop-'+agent&&e.status===200));assert.ok(!JSON.stringify(auditEvents).includes('private'));assert.ok(!JSON.stringify(auditEvents).includes('Case A'))
  const hidden=await fetch(origin+'/api/data?resource=cases&select=internal_notes',{headers:{Cookie:cookie}});assert.equal(hidden.status,400)
  assert.equal((await fetch(origin+'/api/data?resource=cases',{method:'PATCH',headers:{Cookie:cookie}})).status,405)
 }
 const cookie=await signIn('a');consoleConfig.fingerprint='b'.repeat(64)
 const stale=await fetch(origin+'/api/data?resource=cases',{headers:{Cookie:cookie}});assert.equal(stale.status,409);assert.ok((await stale.json()).error.includes('schema changed'))

})
