import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { randomUUID } from 'node:crypto';
import pg from 'pg';
import {captureReadinessProbe} from '../dist/data-repair-readiness-probe.js';
import { operationReceiptSchema, operationRequestFromHeaders, withOperationTransaction, withDataRepairCapture, checkDataRepairCapture,
  customerOperationReceiptSchema, customerOperationRequestFromHeaders, withCustomerOperationTransaction,
  withRepairableOperationTransaction, withRepairableCustomerOperationTransaction, DataRepairCaptureError, OperationCommitUnknownError } from '../dist/index.js';

const enabled=process.env.DATABASE_URL&&process.env.GREGALE_DATA_REPAIR_SCHEMA;

async function dropPrivateDatabase(admin,name){
  for(let attempt=0;attempt<200;attempt++){
    if(Number((await admin.query('SELECT count(*) FROM pg_stat_activity WHERE datname=$1',[name])).rows[0].count)===0)break;
    await new Promise(resolve=>setTimeout(resolve,25));
  }
  await admin.query(`DROP DATABASE "${name}"`);
}

for(const protocol of ['managed','customer'])test(`repairable ${protocol} transactions bind capture, receipts, replay and rollback`,{skip:!enabled},async()=>{
  const admin=new pg.Client({connectionString:process.env.DATABASE_URL});await admin.connect();
  const name='repairable_node_'+randomUUID().replaceAll('-',''),role='repairable_writer_'+randomUUID().replaceAll('-','');
  await admin.query(`CREATE DATABASE "${name}" TEMPLATE template0`);
  const dsn=new URL(process.env.DATABASE_URL);dsn.pathname='/'+name;
  const owner=new pg.Pool({connectionString:dsn.toString()}),writers=new pg.Pool({connectionString:dsn.toString(),max:1});
  let created=false;
  const pool={connect:async()=>{const c=await writers.connect();try{await c.query(`SET ROLE "${role}"`);return c;}catch(e){c.release(true);throw e;}}};
  try{
    await owner.query(await readFile(process.env.GREGALE_DATA_REPAIR_SCHEMA,'utf8'));
    await owner.query(operationReceiptSchema);await owner.query(customerOperationReceiptSchema);
    await owner.query(`CREATE TABLE products(id bigint PRIMARY KEY,category text);SELECT gregale_repair.enroll('products',ARRAY['category']);INSERT INTO products(id,category) VALUES(1,'old'),(2,'old')`);
    await owner.query(`CREATE ROLE "${role}" NOLOGIN`);created=true;
    for(const file of ['writer-grants.sql','repairable-writer-grants.sql']){
      const grants=await readFile(new URL('../../data-repair-tests/'+file,import.meta.url),'utf8');await owner.query(grants.replaceAll('__WRITER_ROLE__',`"${role}"`));
    }
    const fixture=JSON.parse(await readFile(new URL('../../operation-tests/request-fixture.json',import.meta.url),'utf8'));
    const managed=operationRequestFromHeaders(fixture.headers,fixture.method,fixture.path,Buffer.from(fixture.body_base64,'base64'));
    const request=id=>protocol==='managed'?{...managed,operationId:id}:customerOperationRequestFromHeaders({
      'X-Gregale-Customer-Operation-Transaction-Version':'1','X-Gregale-Customer-Operation-Result-Max-Bytes':'65536',
      'X-Gregale-Customer-Operation-Id':id,'X-Gregale-Operation-Attempt':'1','X-Gregale-Operation-Capability':'a'.repeat(64),
      'X-Faas-Invocation-Id':randomUUID(),'X-Faas-Tenant-Id':managed.accountId,'X-Faas-App-Id':managed.appId,'X-Faas-Platform-Tenant-Id':managed.platformTenantId,
    },managed.method,managed.path,managed.body);
    const config={table:'public.products',environment:'production',release:'repairable-node-v1'};
    const run=async(id,handler,cfg=config,selected=pool)=>protocol==='managed'?
      withRepairableOperationTransaction(selected,request(id),cfg,async tx=>({result:await handler(tx)})):
      withRepairableCustomerOperationTransaction(selected,request(id),cfg,handler);
    const edit=async tx=>{await tx.query("UPDATE products SET category='new'");return{changed:2};};
    const facts=async()=>(await owner.query(`SELECT jsonb_build_object('rows',(SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM products p),
      'operations',(SELECT count(*) FROM gregale_repair.operations),'changes',(SELECT count(*) FROM gregale_repair.changes),
      'bindings',(SELECT count(*) FROM gregale_repair.capture_receipts),'managed',(SELECT count(*) FROM public.gregale_operation_inbox),
      'customer',(SELECT count(*) FROM public.gregale_customer_operation_inbox)) AS value`)).rows[0].value;
    const operation=randomUUID(),first=await run(operation,edit);assert.equal(first.replayed,false);
    await owner.query("UPDATE products SET category='human' WHERE id=1");let before=await facts();
    const replay=await run(operation,async()=>assert.fail('committed callback reran'));assert.equal(replay.replayed,true);assert.equal(replay.body,first.body);assert.deepEqual(await facts(),before);
    const scope={account_id:managed.accountId,app_id:managed.appId,platform_tenant_id:managed.platformTenantId,environment:config.environment};
    const plan=(await owner.query('SELECT gregale_repair.create_plan($1,$2::jsonb,$3) AS value',[operation,JSON.stringify(scope),'a'.repeat(64)])).rows[0].value;
    assert.equal(plan.candidates,1);assert.equal(plan.conflicts,1);
    for(const change of [{release:'other'},{environment:'staging'},{table:'public.other'}]){
      await assert.rejects(run(operation,edit,{...config,...change}),e=>e instanceof DataRepairCaptureError&&e.code==='repair_capture_mismatch');
    }
    before=await facts();await assert.rejects(run(randomUUID(),async tx=>{await edit(tx);throw Error('business validation failed');}),/business validation failed/);assert.deepEqual(await facts(),before);
    const faultPool={connect:async()=>{const c=await pool.connect();return{release:d=>c.release(d),query:(sql,values)=>{
      if(sql.startsWith('SELECT gregale_repair.capture_transaction')&&values[7]==='seal'){values=[...values];values[6]=Buffer.alloc(32);}
      return c.query(sql,values);
    }};}};
    await assert.rejects(run(randomUUID(),edit,config,faultPool),e=>e.code==='repair_capture_mismatch');assert.deepEqual(await facts(),before);
    const incomplete=randomUUID();await assert.rejects(run(incomplete,async tx=>{const result=await edit(tx);await tx.query('SELECT gregale_repair.finish_operation($1)',[incomplete]);return result;}),e=>e.code==='repair_capture_incomplete');assert.deepEqual(await facts(),before);
    const zero=randomUUID();await run(zero,async()=>({changed:0}));assert.equal((await owner.query('SELECT count(*)::int AS n FROM gregale_repair.changes WHERE operation_id=$1',[zero])).rows[0].n,0);
    const legacy=randomUUID();if(protocol==='managed')await withOperationTransaction(pool,request(legacy),async()=>({result:{old:true}}));else await withCustomerOperationTransaction(pool,request(legacy),async()=>({old:true}));
    before=await facts();await assert.rejects(run(legacy,async()=>assert.fail('legacy callback reran')),e=>e.code==='repair_capture_missing');assert.deepEqual(await facts(),before);
    let lose=true;const lostPool={connect:async()=>{const c=await pool.connect();return{release:d=>c.release(d),query:async(sql,values)=>{
      const result=await c.query(sql,values);if(sql==='COMMIT'&&lose){lose=false;throw Error('lost commit response');}return result;
    }};}};
    const lost=randomUUID();await assert.rejects(run(lost,edit,config,lostPool),OperationCommitUnknownError);before=await facts();
    assert.equal((await run(lost,async()=>assert.fail('unknown commit reran'))).replayed,true);assert.deepEqual(await facts(),before);
    await owner.query(`REVOKE EXECUTE ON FUNCTION gregale_repair.capture_transaction(uuid,jsonb,text,text,text,bytea,bytea,text) FROM "${role}"`);
    await assert.rejects(run(randomUUID(),async()=>assert.fail('unavailable capture reached callback')),e=>e.code==='repair_capture_unavailable');assert.deepEqual(await facts(),before);
    await owner.query(`GRANT EXECUTE ON FUNCTION gregale_repair.capture_transaction(uuid,jsonb,text,text,text,bytea,bytea,text) TO "${role}"`);
    const inbox=protocol==='managed'?'public.gregale_operation_inbox':'public.gregale_customer_operation_inbox';
    await owner.query(`UPDATE ${inbox} SET response_body=response_body||' ' WHERE operation_id=$1`,[operation]);
    await assert.rejects(run(operation,async()=>assert.fail('mismatched capture reran')),e=>e.code==='repair_capture_mismatch');
  }finally{
    await writers.end();if(created)await owner.query(`DROP OWNED BY "${role}";DROP ROLE "${role}"`);await owner.end();
    try{await dropPrivateDatabase(admin,name);}finally{await admin.end();}
  }
});
test('capture readiness timeouts discard late and busy writer leases without exposing errors',async()=>{
  const previous=captureReadinessProbe.timeout_ms;captureReadinessProbe.timeout_ms=20;
  const config={accountId:randomUUID(),appId:randomUUID(),table:'public.products',environment:'production',release:'timeout-v1'};
  try {
    let finish,queries=0;const releases=[];
    const pending=checkDataRepairCapture({connect:()=>new Promise(resolve=>{finish=resolve;})},config);
    assert.equal((await pending).status,'unknown');
    finish({query:async()=>{queries++;return{rows:[]};},release:discard=>releases.push(discard)});
    await new Promise(resolve=>setImmediate(resolve));assert.equal(queries,0);assert.deepEqual(releases,[true]);
    let rejectQuery;const busyReleases=[];
    const report=await checkDataRepairCapture({connect:async()=>({query:()=>new Promise((_resolve,reject)=>{rejectQuery=reject;}),
      release:discard=>{busyReleases.push(discard);rejectQuery(Error('private-writer-credential'));}})},config);
    assert.equal(report.status,'unknown');assert.deepEqual(busyReleases,[true]);assert.ok(!JSON.stringify(report).includes('private-writer-credential'));
    const unavailable=await checkDataRepairCapture({connect:async()=>{throw Error('private-writer-credential');}},config);
    assert.equal(unavailable.status,'unknown');assert.ok(!JSON.stringify(unavailable).includes('private-writer-credential'));
  } finally {captureReadinessProbe.timeout_ms=previous;}
});
test('repair capture commits with the operation receipt, replays once, and rolls back callback failure',{skip:!enabled},async()=>{
  const admin=new pg.Client({connectionString:process.env.DATABASE_URL});await admin.connect();
  const name='repair_node_'+randomUUID().replaceAll('-','');await admin.query(`CREATE DATABASE "${name}" TEMPLATE template0`);
  const dsn=new URL(process.env.DATABASE_URL);dsn.pathname='/'+name;
  const pool=new pg.Pool({connectionString:dsn.toString()});
  try {
    await pool.query(await readFile(process.env.GREGALE_DATA_REPAIR_SCHEMA,'utf8'));
    await pool.query(operationReceiptSchema);
    await pool.query(`CREATE TABLE products(id bigint PRIMARY KEY,category text);SELECT gregale_repair.enroll('products',ARRAY['category']);INSERT INTO products(id,category) VALUES(1,'old'),(2,'old')`);
    const fixture=JSON.parse(await readFile(new URL('../../operation-tests/request-fixture.json',import.meta.url),'utf8'));
    const request=operationRequestFromHeaders(fixture.headers,fixture.method,fixture.path,Buffer.from(fixture.body_base64,'base64'));
    const config={table:'products',environment:'production',release:'node-import-v1'};
    const first=await withOperationTransaction(pool,request,tx=>withDataRepairCapture(tx,request,config,async tx=>{
      await tx.query("UPDATE products SET category='new'");return {result:{changed:2}};
    }));
    assert.equal(first.replayed,false);
    const replay=await withOperationTransaction(pool,{...request,generation:'2'},async()=>{throw Error('committed callback reran');});
    assert.equal(replay.replayed,true);assert.equal(replay.body,first.body);
    const facts=(await pool.query('SELECT count(*)::int AS changes,min(before_version)::text AS before,max(after_version)::text AS after FROM gregale_repair.changes')).rows[0];
    assert.deepEqual(facts,{changes:2,before:'1',after:'2'});
    const failed={...request,operationId:randomUUID()};
    await assert.rejects(withOperationTransaction(pool,failed,tx=>withDataRepairCapture(tx,failed,config,async tx=>{
      await tx.query("UPDATE products SET category='failed'");throw Error('validation failed');
    })),/validation failed/);
    assert.equal((await pool.query('SELECT count(*)::int AS n FROM gregale_repair.operations')).rows[0].n,1);
    assert.equal((await pool.query("SELECT count(*)::int AS n FROM products WHERE category='new' AND gregale_row_version=2")).rows[0].n,2);
  } finally {
    await pool.end();
    // pg clients may still be completing socket teardown after pool.end().
    // Wait for those private sessions before dropping, as the other SDK gates do.
    for(let attempt=0;attempt<200;attempt++){
      const active=Number((await admin.query('SELECT count(*) FROM pg_stat_activity WHERE datname=$1',[name])).rows[0].count);
      if(active===0)break;
      await new Promise(resolve=>setTimeout(resolve,25));
    }
    try{await admin.query(`DROP DATABASE "${name}"`);}finally{await admin.end();}
  }
});

test('capture readiness observes restricted writer sessions without writes or repair authority',{skip:!enabled},async()=>{
  const admin=new pg.Client({connectionString:process.env.DATABASE_URL});await admin.connect();
  const name='repair_writer_node_'+randomUUID().replaceAll('-',''),role='writer_node_'+randomUUID().replaceAll('-','');
  await admin.query(`CREATE DATABASE "${name}" TEMPLATE template0`);
  const dsn=new URL(process.env.DATABASE_URL);dsn.pathname='/'+name;
  const owner=new pg.Pool({connectionString:dsn.toString()}),writers=new pg.Pool({connectionString:dsn.toString(),max:1});
  let roleCreated=false;
  const writerPool=(setting='')=>({connect:async()=>{
    const client=await writers.connect();
    try {await client.query('RESET ROLE');await client.query("SET session_replication_role='origin';SET default_transaction_read_only='off'");
      if(setting)await client.query(setting);
      await client.query(`SET ROLE "${role}"`);return client;
    }catch(error){client.release(true);throw error;}
  }});
  try {
    await owner.query(await readFile(process.env.GREGALE_DATA_REPAIR_SCHEMA,'utf8'));await owner.query(operationReceiptSchema);
    await owner.query(`CREATE TABLE public.gregale_customer_operation_inbox(LIKE public.gregale_operation_inbox INCLUDING ALL);
      ALTER TABLE public.gregale_customer_operation_inbox ALTER COLUMN platform_tenant_id SET NOT NULL;
      CREATE TABLE products(id bigint PRIMARY KEY,category text);SELECT gregale_repair.enroll('products',ARRAY['category']);INSERT INTO products(id,category) VALUES(1,'old'),(2,'old')`);
    await owner.query(`CREATE ROLE "${role}" NOLOGIN`);roleCreated=true;
    const grants=await readFile(new URL('../../data-repair-tests/writer-grants.sql',import.meta.url),'utf8');
    await owner.query(grants.replaceAll('__WRITER_ROLE__',`"${role}"`));
    const fixture=JSON.parse(await readFile(new URL('../../operation-tests/request-fixture.json',import.meta.url),'utf8'));
    const request=operationRequestFromHeaders(fixture.headers,fixture.method,fixture.path,Buffer.from(fixture.body_base64,'base64'));
    const config={accountId:request.accountId,appId:request.appId,platformTenantId:request.platformTenantId,
      table:'public.products',environment:'production',release:'node-writer-v1'};
    const facts=async()=>(await owner.query(`SELECT jsonb_build_object('rows',jsonb_agg(to_jsonb(p) ORDER BY id),
      'operations',(SELECT count(*) FROM gregale_repair.operations),'changes',(SELECT count(*) FROM gregale_repair.changes),
      'plans',(SELECT count(*) FROM gregale_repair.plans),'receipts',(SELECT count(*) FROM public.gregale_operation_inbox)) AS snapshot FROM products p`)).rows[0].snapshot;
    const check=async(status,code,pool=writerPool(),options=config)=>{
      const report=await checkDataRepairCapture(pool,options);assert.equal(report.profile,'capture');assert.equal(report.status,status,JSON.stringify(report));
      assert.equal(report.findings.length,10);assert.equal(report.scope.account_id,request.accountId);
      if(code)assert.ok(report.findings.some(f=>f.code===code),JSON.stringify(report));return report;
    };
    const before=await facts();await check('ready');await check('ready','',writerPool(),{...config,receiptProtocol:'customer'});assert.deepEqual(await facts(),before);
    const first=await withOperationTransaction(writerPool(),request,tx=>withDataRepairCapture(tx,request,config,async tx=>{
      await tx.query("UPDATE products SET category='new'");return {result:{changed:2}};
    }));assert.equal(first.replayed,false);
    const captured=await facts();assert.equal(captured.changes,2);assert.equal(captured.receipts,1);
    await owner.query(`REVOKE EXECUTE ON FUNCTION gregale_repair.finish_operation(uuid) FROM "${role}"`);
    const missing=await check('blocked','writer_permissions_missing');
    assert.ok(missing.findings.find(f=>f.id==='permissions').objects.includes('gregale_repair.finish_operation: EXECUTE'));
    await owner.query(`GRANT EXECUTE ON FUNCTION gregale_repair.finish_operation(uuid) TO "${role}";REVOKE INSERT ON public.gregale_operation_inbox FROM "${role}"`);
    await check('blocked','writer_permissions_missing');
    await owner.query(`GRANT INSERT ON public.gregale_operation_inbox TO "${role}";ALTER TABLE products DISABLE TRIGGER gregale_repair_capture`);
    await check('blocked','triggers_invalid');await owner.query('ALTER TABLE products ENABLE TRIGGER gregale_repair_capture');
    await check('blocked','capture_session_invalid',writerPool("SET session_replication_role='replica'"));
    await check('blocked','capture_session_invalid',writerPool("SET default_transaction_read_only='on'"));
    await check('ready');assert.deepEqual(await facts(),captured);
    const client=await writerPool().connect();try{await assert.rejects(client.query("SELECT gregale_repair.create_plan(gen_random_uuid(),'{}','')"),/permission denied/);}finally{client.release(true);}
    await assert.rejects(checkDataRepairCapture({connect(){throw Error('invalid options reached a database');}},{...config,table:'products'}),/invalid data repair capture readiness/);
  } finally {
    await writers.end();
    if(roleCreated){await owner.query(`DROP OWNED BY "${role}";DROP ROLE "${role}"`);}
    await owner.end();
    try{await dropPrivateDatabase(admin,name);}finally{await admin.end();}
  }
});
