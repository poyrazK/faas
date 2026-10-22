import test from 'node:test'
import assert from 'node:assert/strict'
import {createRequire} from 'node:module'
import {pathToFileURL} from 'node:url'
import {join} from 'node:path'
import {mkdtemp,readFile,rm,readdir} from 'node:fs/promises'
import {randomUUID,randomBytes} from 'node:crypto'
const directory=process.env.DATA_API_SUPPORT_CONSOLE_DIR
test('PostgreSQL audit persists history, isolates tenants, retries outages and replays outbox after restart',{skip:!directory||!process.env.DATA_API_TEST_DATABASE_URL,timeout:30000},async t=>{
 const pg=createRequire(join(directory,'package.json'))('pg'),{postgresAudit}=await import(pathToFileURL(join(directory,'audit-postgres.mjs')))
 const suffix=randomBytes(5).toString('hex'),db='support_audit_'+suffix,role='audit_writer_'+suffix,password=randomBytes(16).toString('hex'),admin=new pg.Client({connectionString:process.env.DATA_API_TEST_DATABASE_URL});await admin.connect()
 const outbox=await mkdtemp('/tmp/gregale-audit-outbox-');let owner,store,services=[]
 t.after(async()=>{
  await store?.close()
  for(const service of services)if(service.listening){service.closeAllConnections();await new Promise(resolve=>service.close(resolve))}
  // HTTP close starts asynchronous audit-pool cleanup. Wait for its actual
  // connections to retire before DROP DATABASE can terminate a leased client.
  let connections
  for(let attempt=0;attempt<250;attempt++){
   connections=Number((await admin.query('SELECT count(*) FROM pg_stat_activity WHERE datname=$1 AND usename=$2',[db,role])).rows[0].count)
   if(connections===0)break
   await new Promise(resolve=>setTimeout(resolve,20))
  }
  assert.equal(connections,0,'Support console audit connections must close before database teardown')
  await owner?.end();await admin.query('DROP DATABASE IF EXISTS "'+db+'" WITH (FORCE)');await admin.query('DROP ROLE IF EXISTS "'+role+'"');await admin.end();await rm(outbox,{recursive:true,force:true})
 })
 await admin.query('CREATE ROLE "'+role+'" LOGIN NOSUPERUSER NOBYPASSRLS PASSWORD \''+password+'\'')
 await admin.query('CREATE DATABASE "'+db+'"')
 const ownerURL=new URL(process.env.DATA_API_TEST_DATABASE_URL);ownerURL.pathname='/'+db;owner=new pg.Client({connectionString:ownerURL.toString()});await owner.connect()
 await owner.query(await readFile(join(directory,'migrations/audit.sql'),'utf8'))
 await owner.query('GRANT USAGE ON SCHEMA support_audit TO "'+role+'"; GRANT SELECT,INSERT,DELETE ON support_audit.events TO "'+role+'"')
 const loginURL=new URL(ownerURL);loginURL.username=role;loginURL.password=password
 const config={url:loginURL.toString(),outbox,days:7}
 store=await postgresAudit(config)
 const event=(tenant,action='customer.profile')=>({id:randomUUID(),time:new Date().toISOString(),subject:'staff',tenant,action,outcome:'success',status:200,resource:'customers'})
 const a=event('a'),b=event('b');assert.equal(await store.record(a),true);assert.equal(await store.record(b),true)
 const until=async predicate=>{for(let i=0;i<200;i++){await store.flush();if(await predicate())return;await new Promise(resolve=>setTimeout(resolve,20))}assert.fail('Audit delivery did not settle')}
 await until(()=>store.status().pending===0)
 assert.deepEqual((await store.read({tenant:'a'})).map(e=>e.id),[a.id]);assert.equal((await store.read({tenant:'a'},{subject:'other'})).length,0)
 await store.close();store=await postgresAudit(config);assert.equal((await store.read({tenant:'a'})).length,1)
 await owner.query('REVOKE INSERT ON support_audit.events FROM "'+role+'"')
 const pending=event('a','data.export_page');await store.record(pending)
 await until(()=>store.status().last_failure!==null);assert.equal(store.status().pending,1);assert.ok((await readdir(outbox)).includes(pending.id+'.json'))
 await store.close();store=await postgresAudit(config)
 await owner.query('GRANT INSERT ON support_audit.events TO "'+role+'"')
 await until(()=>store.status().pending===0)
 assert.equal((await store.read({tenant:'a'})).length,2)
 assert.equal((await store.read({tenant:'a'},{action:'data.export_page'}))[0].id,pending.id)
 // Replay a committed ID after an ambiguous acknowledgment; it must not duplicate.
 await store.record(pending);await until(()=>store.status().pending===0);assert.equal((await store.read({tenant:'a'})).length,2)
 const old={...event('a'),time:new Date(Date.now()-8*86400000).toISOString()};await store.record(old);await until(()=>store.status().pending===0);assert.equal((await store.read({tenant:'a'})).length,2)
 const direct=new pg.Client({connectionString:loginURL.toString()});await direct.connect();assert.equal((await direct.query('SELECT * FROM support_audit.events')).rows.length,0);await direct.end()
 await assert.rejects(()=>postgresAudit({...config,url:ownerURL.toString()}),/non-owner/)
 await store.close();store=null
 const {demoProvider,demoData,listen}=await import(pathToFileURL(join(directory,'demo.mjs'))),{createConsole}=await import(pathToFileURL(join(directory,'server.mjs'))),{settings}=await import(pathToFileURL(join(directory,'auth.mjs')))
 const provider=await demoProvider(),data=await demoData(provider),configConsole=settings({SUPPORT_ORIGIN:'http://127.0.0.1:1',SUPPORT_OIDC_ISSUER:provider.issuer,SUPPORT_OIDC_CLIENT_ID:'support-console-demo',SUPPORT_DATA_AUDIENCE:'support-data-demo',SUPPORT_DATA_API_URL:data.url,SUPPORT_SCHEMA_FINGERPRINT:'a'.repeat(64),SUPPORT_SESSION_KEY:randomBytes(32).toString('base64url'),SUPPORT_AUDIT_DATABASE_URL:config.url,SUPPORT_AUDIT_OUTBOX_DIR:outbox,SUPPORT_AUDIT_ADMINS:'[{"subject":"agent-a","tenant":"shop-a"}]'})
 const definition=JSON.parse(await readFile(join(directory,'support-console.json'),'utf8')),server=await createConsole(configConsole,definition,{auditSink:()=>{}})
 await listen(server);configConsole.origin='http://127.0.0.1:'+server.address().port
 services=[server,data.server,provider.server]
 const start=await fetch(configConsole.origin+'/auth/login',{redirect:'manual'}),loginCookie=start.headers.getSetCookie()[0].split(';')[0],authorize=new URL(start.headers.get('location'));authorize.searchParams.set('agent','a')
 const authorized=await fetch(authorize,{redirect:'manual'}),callback=await fetch(authorized.headers.get('location'),{redirect:'manual',headers:{Cookie:loginCookie}}),cookie=callback.headers.getSetCookie().find(v=>v.startsWith('support_session=')).split(';')[0]
 const response=await fetch(configConsole.origin+'/api/data?resource=cases&purpose=export',{headers:{Cookie:cookie}});assert.equal(response.status,200)
 let history
 for(let i=0;i<100;i++){history=await(await fetch(configConsole.origin+'/api/audit?action=data.export_page',{headers:{Cookie:cookie}})).json();if(history.events?.length)break;await new Promise(resolve=>setTimeout(resolve,20))}
 assert.equal(history.delivery.mode,'postgres');assert.equal(history.events[0].action,'data.export_page')
 const csv=await fetch(configConsole.origin+'/api/audit?format=csv&action=data.export_page',{headers:{Cookie:cookie}});assert.equal(csv.status,200);assert.ok((await csv.text()).includes('data.export_page'))
 // A broken persistent volume must block release of data.
 await rm(outbox,{recursive:true,force:true});const {writeFile}=await import('node:fs/promises');await writeFile(outbox,'unwritable directory')
 assert.equal((await fetch(configConsole.origin+'/api/data?resource=cases',{headers:{Cookie:cookie}})).status,503)
 await rm(outbox,{force:true})
 for(const service of [server,data.server,provider.server]){service.closeAllConnections();await new Promise(resolve=>service.close(resolve))}

})
