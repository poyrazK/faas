import test from 'node:test'
import assert from 'node:assert/strict'
import net from 'node:net'
import { spawn, execFile } from 'node:child_process'
import { promisify } from 'node:util'
import { once } from 'node:events'
import { mkdtemp, mkdir, readFile, writeFile, symlink, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { pathToFileURL } from 'node:url'
import { createRequire } from 'node:module'
import { createDataClient, validatedRpc } from '../../sdk/data/dist/index.js'
const execute=promisify(execFile)
const enabled=Boolean(process.env.DATA_API_TEST_DATABASE_URL&&process.env.DATA_API_POSTGREST_BIN&&process.env.DATA_API_RUNTIME_DIR&&process.env.DATA_API_DEV_CLI)
async function port(){const server=net.createServer();await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));const value=server.address().port;await new Promise(resolve=>server.close(resolve));return value}
test('transaction starter generates typed clients, rolls back partial work, isolates tenants and serializes stock and retries',{skip:!enabled,timeout:120000},async t=>{
 const root=await mkdtemp(join(tmpdir(),'gregale-transaction-'));t.after(()=>rm(root,{recursive:true,force:true}))
 const output=join(root,'package')
 await execute(process.env.DATA_API_DEV_CLI,['data-api','transaction','init','--name','place_order','--output',output])
 const runtime=pathToFileURL(process.env.DATA_API_RUNTIME_DIR+'/'),require=createRequire(new URL('package.json',runtime)),pg=require('pg')
 const {inspect,fingerprint}=await import(new URL('types.mjs',runtime))
 const {generateValidators}=await import(new URL('type-render.mjs',runtime))
 const {permissions}=await import(new URL('rpc-permissions.mjs',runtime))
 const {runtimeConfig}=await import(new URL('config.mjs',runtime)),{createServer,tokenVerifier}=await import(new URL('server.mjs',runtime))
 const {generateKeyPair,exportJWK,createLocalJWKSet,SignJWT}=await import(pathToFileURL(require.resolve('jose')))
 const suffix=`${process.pid}_${Date.now()}`,database=`transactions_${suffix}`,role=`tx_login_${suffix}`
 const admin=new pg.Client({connectionString:process.env.DATA_API_TEST_DATABASE_URL});await admin.connect()
 let owner,child,gateway
 t.after(async()=>{if(gateway){gateway.closeAllConnections();await new Promise(resolve=>gateway.close(resolve))};if(child&&child.exitCode===null){child.kill();await once(child,'exit')};await owner?.end();await admin.query(`DROP DATABASE IF EXISTS "${database}" WITH(FORCE)`);await admin.query(`DROP ROLE IF EXISTS "${role}"`);await admin.end()})
 await admin.query(`CREATE ROLE "${role}" LOGIN NOINHERIT NOCREATEDB NOCREATEROLE NOBYPASSRLS PASSWORD 'test-only'`)
 await admin.query(`ALTER ROLE "${role}" SET statement_timeout=15000`)
 await admin.query(`CREATE DATABASE "${database}"`)
 await admin.query(`REVOKE CREATE,TEMPORARY ON DATABASE "${database}" FROM PUBLIC`)
 const ownerURL=new URL(process.env.DATA_API_TEST_DATABASE_URL);ownerURL.pathname=`/${database}`
 owner=new pg.Client({connectionString:ownerURL.toString()});await owner.connect()
 await owner.query('REVOKE ALL ON SCHEMA public FROM PUBLIC')
 await owner.query(await readFile(join(output,'migration.sql'),'utf8'))
 await owner.query('BEGIN');await owner.query(await readFile(join(output,'fixture.sql'),'utf8'));await owner.query('COMMIT')
 await owner.query(`GRANT USAGE ON SCHEMA api TO "${role}"; GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA api TO "${role}"; ALTER DEFAULT PRIVILEGES IN SCHEMA api GRANT SELECT,INSERT,UPDATE,DELETE ON TABLES TO "${role}"; ALTER DEFAULT PRIVILEGES IN SCHEMA api GRANT USAGE,SELECT ON SEQUENCES TO "${role}"`)
 const permissionPlan=await permissions(ownerURL.toString(),{role,apply:false});assert.deepEqual(permissionPlan.blockers,[])
 await permissions(ownerURL.toString(),{role,apply:true})
 const loginURL=new URL(ownerURL);loginURL.username=role;loginURL.password='test-only'
 const snapshot=await inspect(loginURL.toString(),['api'])
 await writeFile(join(output,'database.types.ts'),generateValidators(snapshot))
 await writeFile(join(output,'package.json'),' {"type":"module"}')
 await mkdir(join(output,'node_modules/@gregale'),{recursive:true});await symlink(resolve('sdk/data'),join(output,'node_modules/@gregale/data'),'dir')
 await writeFile(join(output,'tsconfig.json'),JSON.stringify({compilerOptions:{target:'ES2022',module:'NodeNext',moduleResolution:'NodeNext',strict:true,skipLibCheck:true,outDir:'dist'},include:['*.ts']}))
 await execute(resolve('sdk/data/node_modules/.bin/tsc'),['-p',join(output,'tsconfig.json')],{timeout:30000})
 const {transactionArguments}=await import(pathToFileURL(join(output,'dist/client.js')))
 const {rpcValidators}=await import(pathToFileURL(join(output,'dist/database.types.js')))
 const config=runtimeConfig({DATABASE_URL:loginURL.toString(),DATA_API_ISSUER:'https://issuer.example',DATA_API_JWKS_URL:'https://issuer.example/jwks',DATA_API_AUDIENCE:'orders'});config.snapshot=snapshot;config.functions=snapshot.functions;config.fingerprint=fingerprint(snapshot)
 const upstream=await port(),ready=await port()
 child=spawn(process.env.DATA_API_POSTGREST_BIN,[],{env:{...process.env,...config.postgrestEnv,PGRST_SERVER_PORT:String(upstream),PGRST_ADMIN_SERVER_PORT:String(ready)},stdio:['ignore','pipe','pipe']});child.stdout.on('data',()=>{});child.stderr.on('data',()=>{})
 const {privateKey,publicKey}=await generateKeyPair('ES256'),jwk=await exportJWK(publicKey)
 gateway=createServer(config,tokenVerifier(config.auth,createLocalJWKSet({keys:[jwk]})),upstream,ready,()=>{})
 await new Promise(resolve=>gateway.listen(0,'127.0.0.1',resolve));const url=`http://127.0.0.1:${gateway.address().port}`
 for(let i=0;i<100&&(await fetch(url+'/healthz')).status!==200;i++)await new Promise(resolve=>setTimeout(resolve,50))
 const sign=async(sub,tenant)=>new SignJWT({sub,...(tenant?{tenant_id:tenant}:{}),role:'postgres'}).setProtectedHeader({alg:'ES256'}).setIssuer(config.auth.issuer).setAudience(config.auth.audience).setExpirationTime('5m').sign(privateKey)
 const client=async(sub,tenant)=>createDataClient({url,accessToken:await sign(sub,tenant)}).schema('api')
 const alice=await client('alice','alpha'),bob=await client('bob','beta'),missing=await client('outsider')
 const args=(quantities=[2],skus=['a_widget'],key=crypto.randomUUID())=>({request_key:key,skus,quantities})
 let constructed=0
 const invalid=await validatedRpc({tenant_id:'beta',...args()},transactionArguments,value=>{constructed++;return alice.rpc('place_order',value)})
 assert.equal(invalid.outcome,'validation_failed');assert.equal(constructed,0)
 for(const value of [{skus:['a_widget'],quantities:[1]},{...args(),skus:[4]},{...args(),quantities:['1']}]) {
  const rejected=await validatedRpc(value,rpcValidators.api.place_order,parsed=>{constructed++;return alice.rpc('place_order',parsed)})
  assert.equal(rejected.outcome,'validation_failed');assert.equal(constructed,0)
 }
 const shortage=await alice.rpc('place_order',args([2,2],['a_widget','z_widget']));assert.equal(shortage.status,409)
 assert.equal((await owner.query("SELECT stock FROM api.place_order_inventory WHERE tenant_id='alpha' AND sku='a_widget'")).rows[0].stock,10)
 assert.equal((await owner.query('SELECT count(*)::int AS count FROM api.place_order_orders')).rows[0].count,0)
 assert.equal((await owner.query('SELECT count(*)::int AS count FROM api.place_order_items')).rows[0].count,0)
 const request=args(),first=await validatedRpc(request,transactionArguments,value=>alice.rpc('place_order',value));assert.equal(first.outcome,'completed');assert.equal(first.result.error,null);assert.equal(first.result.data[0].total_cents,1000)
 const receipt=first.result.data[0]
 const replay=await alice.rpc('place_order',request);assert.equal(replay.data[0].id,receipt.id)
 assert.equal((await alice.rpc('place_order',{...request,quantities:[1]})).status,409)
 assert.deepEqual((await bob.from('place_order_orders').select('id')).data,[])
 assert.equal((await missing.rpc('place_order',args())).status,403)
 const blocked=await alice.from('place_order_inventory').update({stock:999}).eq('sku','a_widget').select('stock');assert.equal(blocked.error,null);assert.deepEqual(blocked.data,[])
 const remaining=await alice.from('place_order_inventory').select('stock').eq('sku','a_widget');assert.equal(remaining.data[0].stock,8)
 const concurrent=args([1],['z_widget']);const retries=await Promise.all([alice.rpc('place_order',concurrent),alice.rpc('place_order',concurrent)]);assert.ok(retries.every(result=>result.error===null));assert.equal(retries[0].data[0].id,retries[1].data[0].id)
 await owner.query("UPDATE api.place_order_inventory SET stock=1 WHERE tenant_id='alpha' AND sku='z_widget'")
 const competed=await Promise.all([alice.rpc('place_order',args([1],['z_widget'])),alice.rpc('place_order',args([1],['z_widget']))]);assert.equal(competed.filter(result=>result.error===null).length,1);assert.equal(competed.filter(result=>result.status===409).length,1)
 assert.equal((await owner.query("SELECT stock FROM api.place_order_inventory WHERE tenant_id='alpha' AND sku='z_widget'")).rows[0].stock,0)
 const checks=JSON.parse(await readFile(join(output,'data-checks.json'),'utf8'));for(const check of checks.checks){assert.equal((await owner.query(check.sql)).rows[0].passed,true)}
 await owner.query('TRUNCATE api.place_order_items,api.place_order_orders,api.place_order_inventory')
 await owner.query('BEGIN');await owner.query(await readFile(join(output,'fixture.sql'),'utf8'));await owner.query('COMMIT')
 const {prepareAccessChecks,runAccessChecks}=await import(new URL('dev-access.mjs',runtime))
 const scenarios=await prepareAccessChecks(join(output,'access-checks.json'),{identities:{alice:{},bob:{}}})
 const results=await runAccessChecks(scenarios,{url,identities:{alice:{token:await sign('alice','alpha')},bob:{token:await sign('bob','beta')}},signal:AbortSignal.timeout(30000)})
 assert.ok(results.every(result=>result.passed),JSON.stringify(results))
 const direct=await alice.from('place_order_orders').insert({tenant_id:'alpha',created_by:'alice',request_key:crypto.randomUUID(),request_payload:[]}).select('id')
 assert.equal(direct.status,403)

})
