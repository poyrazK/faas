// Opt-in integration: real PostgreSQL and mixed Node/Python workers, protocol fixtures for native HTTP.
import test from 'node:test';
import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import {createInterface} from 'node:readline';
import {spawn,execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {mkdtemp,readFile,rm} from 'node:fs/promises';
import {join,resolve} from 'node:path';
import {tmpdir} from 'node:os';
import {fileURLToPath} from 'node:url';
import {createHash,randomUUID} from 'node:crypto';
const root=resolve(fileURLToPath(new URL('.',import.meta.url)),'../../..'),exec=promisify(execFile),sleep=ms=>new Promise(r=>setTimeout(r,ms));
const hash=v=>createHash('sha256').update(v).digest('hex');
test('transactional events: PostgreSQL rollback, mixed workers, controls, native evidence and isolation',{skip:process.env.GREGALE_EVENT_OUTBOX_POSTGRES!=='1',timeout:240000},async t=>{
  const home=await mkdtemp(join(tmpdir(),'gregale-event-outbox-')),closers=[];
  t.after(async()=>{try{for(const close of closers.reverse())await close();}finally{await rm(home,{recursive:true,force:true});}});
  const pgBin=process.env.GREGALE_LOCAL_PG_BIN??'/opt/homebrew/opt/postgresql@16/bin',python=process.env.GREGALE_LOCAL_PYTHON??join(root,'sdk/python/.venv/bin/python');
  await exec(join(pgBin,'initdb'),['-D',join(home,'pg'),'-U','sdk','-A','trust','--no-locale','-E','UTF8']);
  const postgres=spawn(join(pgBin,'postgres'),['-D',join(home,'pg'),'-k',home,'-h',''],{stdio:'ignore'});closers.push(async()=>{const ended=new Promise(r=>postgres.once('exit',r));postgres.kill();await ended;});
  const database=`postgresql://sdk@localhost/postgres?host=${encodeURIComponent(home)}`;
  const {default:pg}=await import('../node_modules/pg/lib/index.js'),pool=new pg.Pool({connectionString:database,statement_timeout:5000});closers.push(()=>pool.end());
  for(let i=0;;i++){try{await pool.query('SELECT 1');break;}catch(e){if(i>200)throw e;await sleep(20);}}
  const sdk=await import('../dist/preview.js'),f=JSON.parse(await readFile(join(root,'sdk/request-contracts/event-outbox.json'),'utf8'));
  await pool.query(sdk.eventOutboxSchema);
  // Simulate an existing Go installation, then explicitly apply the exported upgrade.
  await pool.query('ALTER TABLE gregale_event_outbox DROP CONSTRAINT gregale_event_outbox_request_body_check');
  await pool.query("ALTER TABLE gregale_event_outbox ADD CONSTRAINT gregale_event_outbox_request_body_check CHECK(octet_length(request_body) BETWEEN 1 AND 1048576 AND request_body::jsonb IS NOT NULL)");
  await pool.query(sdk.eventOutboxSchema);await pool.query('CREATE TABLE outbox_business(key text PRIMARY KEY)');
  const native=new Map(),posts=new Map();let delay=false;
  const server=createServer(async(req,res)=>{
    const send=(code,body)=>{res.writeHead(code,{'content-type':'application/json'});res.end(JSON.stringify(body));};
    if(req.headers.authorization!=='Bearer fixture-owner'){send(403,{});return;}
    const chunks=[];for await(const chunk of req)chunks.push(chunk);const body=Buffer.concat(chunks).toString(),input=JSON.parse(body);
    if(input.expected_app_id!==f.scope.app_id){send(409,{});return;}
    const id='key.'+hash(input.key),source='app.'+f.scope.app_id,old=native.get(input.key),digest=hash(body);
    if(req.url.endsWith('/verify-publication')){send(200,{app_id:f.scope.app_id,source,event_id:id,status:old?(old.digest===digest?'match':'conflict'):'unavailable',observed_at:new Date().toISOString(),...(old?{receipt:old.value.receipt}: {})});return;}
    assert.ok(req.url.endsWith('/events:publish'));posts.set(input.key,(posts.get(input.key)??0)+1);
    if(old&&old.digest!==digest){send(409,{});return;}
    const value=old?.value??{app_id:f.scope.app_id,source,duplicate:false,receipt:{account_id:f.scope.account_id,id,accepted_at:new Date().toISOString()}};
    native.set(input.key,{digest,value,input});if(input.key.startsWith('lost')&&!old){res.destroy();return;}
    if(delay)await sleep(100);send(200,{...value,duplicate:!!old});
  });await new Promise(r=>server.listen(0,'127.0.0.1',r));closers.push(()=>new Promise(r=>server.close(r)));
  const scope={...f.scope,base_url:`http://127.0.0.1:${server.address().port}`},contract=new sdk.EventContract(f.contract);
  const outboxPool={async connect(){const connection=await pool.connect();return {async query(sql,values){try{return await connection.query(sql,values);}catch(error){t.diagnostic('Fixture SQL error: '+error.message);throw error;}},release:discard=>connection.release(discard)};}};
  const publisher=sdk.createEventPublisher(contract,{scope}),outbox=sdk.createEventOutbox(outboxPool,{scope,token:'fixture-owner'});
  const child=spawn(python,[join(root,'examples/sdk-csv-jobs/outbox_driver.py'),'--stdio'],{env:{...process.env,DATABASE_URL:database,GREGALE_PIPELINE_SCOPE:JSON.stringify(scope),PYTHONPATH:join(root,'sdk/python')},stdio:['pipe','pipe','pipe']});
  let seq=0,errors='';child.stderr.on('data',b=>errors+=b);const pending=new Map(),lines=createInterface({input:child.stdout});
  let ready;const startup=new Promise((resolve,reject)=>{const timer=setTimeout(()=>reject(Error('Python startup timeout '+errors)),120000);ready=()=>{clearTimeout(timer);resolve();};child.once('exit',code=>{clearTimeout(timer);reject(Error('Python exit '+code+' '+errors));});});
  lines.on('line',line=>{const packet=JSON.parse(line);if(packet.ready){ready();return;}pending.get(packet.request_id)?.(packet.result);pending.delete(packet.request_id);});
  closers.push(async()=>{lines.close();if(child.exitCode===null){const ended=new Promise(r=>child.once('exit',r));child.kill();await ended;}});await startup;
  const py=request=>new Promise((resolve,reject)=>{const request_id=String(seq++),timer=setTimeout(()=>{pending.delete(request_id);reject(Error('Python request timeout '+errors));},20000);pending.set(request_id,result=>{clearTimeout(timer);resolve(result);});child.stdin.write(JSON.stringify({request_id,...request})+'\n');});
  const stage=async(key,{rollback=false,environment='staging',retryPolicy}={})=>sdk.withEventOutboxTransaction(outboxPool,async tx=>{await tx.query('INSERT INTO outbox_business VALUES ($1)',[key]);const value=await publisher.stage(tx,f.input,{key,environment,retryPolicy});if(rollback)throw Error('rollback');return value;},{timeoutMs:10000});
  const pyStage=(key,extra={})=>py({action:'stage',contract:f.contract,input:f.input,options:{key,environment:'staging'},...extra});
  const recover=()=>outbox.recoverNext({timeoutMs:10000,backoffMs:1});
  await assert.rejects(stage('rollback-node',{rollback:true}));assert.equal((await pyStage('rollback-python',{rollback:true})).code,'conflict');
  assert.equal((await pool.query('SELECT count(*)::int AS n FROM outbox_business')).rows[0].n,0);assert.equal((await pool.query('SELECT count(*)::int AS n FROM gregale_event_outbox')).rows[0].n,0);assert.equal(native.size,0);
  await stage('lost-node');assert.equal((await py({action:'recover'})).outcome,'unconfirmed');
  assert.equal((await outbox.reconcile('lost-node',{timeoutMs:10000})).outcome,'published');assert.equal(posts.get('lost-node'),1);
  await pyStage('lost-python');assert.equal((await recover()).outcome,'unconfirmed');assert.equal((await py({action:'reconcile',key:'lost-python'})).outcome,'published');assert.equal(posts.get('lost-python'),1);
  const status=await pyStage('same-key');const repeated=await sdk.withEventOutboxTransaction(outboxPool,tx=>publisher.stage(tx,f.input,{key:'same-key',environment:'staging'}),{timeoutMs:10000});assert.equal(status.retry_until,repeated.retry_until);
  await assert.rejects(sdk.withEventOutboxTransaction(outboxPool,tx=>publisher.stage(tx,f.input,{key:'same-key',environment:'production'}),{timeoutMs:10000}),{code:'conflict'});
  assert.equal((await recover()).outcome,'published');assert.equal(native.get('same-key').input.environment,'staging');
  await stage('stale-lease');await pool.query("UPDATE gregale_event_outbox SET claim_token=decode(repeat('01',32),'hex'),claim_expires_at=clock_timestamp()-interval '1 second' WHERE producer_key='stale-lease'");assert.equal((await py({action:'recover'})).outcome,'published');
  await stage('active-lease');await pool.query("UPDATE gregale_event_outbox SET claim_token=decode(repeat('01',32),'hex'),claim_expires_at=clock_timestamp()+interval '5 seconds' WHERE producer_key='active-lease'");assert.equal((await recover()).outcome,'empty');assert.equal((await py({action:'recover'})).outcome,'empty');await pool.query("UPDATE gregale_event_outbox SET claim_expires_at=clock_timestamp()-interval '1 second' WHERE producer_key='active-lease'");assert.equal((await recover()).outcome,'published');
  for(let i=0;i<8;i++)await(i%2?pyStage('concurrent-'+i):stage('concurrent-'+i));
  delay=true;const concurrent=await Promise.all([outbox.recover({timeoutMs:20000,maxEvents:12,concurrency:4,backoffMs:1}),py({action:'recover'})]);delay=false;
  assert.ok(concurrent[0].some(r=>r.outcome==='published'));for(let i=0;i<8;i++)assert.equal(posts.get('concurrent-'+i),1);assert.equal((await pool.query("SELECT count(*)::int AS n FROM gregale_event_outbox WHERE state<>'published'")).rows[0].n,0);
  await stage('paused');await pool.query("INSERT INTO gregale_event_outbox_publishing_controls(account_id,app_id,paused,revision,updated_at) VALUES ($1,$2,true,1,clock_timestamp())",[scope.account_id,scope.app_id]);assert.equal((await recover()).outcome,'paused');assert.equal((await py({action:'recover'})).outcome,'paused');assert.equal(posts.has('paused'),false);
  await pool.query('DELETE FROM gregale_event_outbox_publishing_controls');
  await pool.query("INSERT INTO gregale_event_outbox_rate_limits(account_id,app_id,enabled,revision,interval_ms,credit_ms,updated_at) VALUES ($1,$2,true,1,3600000,0,clock_timestamp())",[scope.account_id,scope.app_id]);
  assert.equal((await recover()).outcome,'rate_limited');assert.equal((await py({action:'recover'})).outcome,'rate_limited');assert.equal(posts.has('paused'),false);
  await pool.query('UPDATE gregale_event_outbox_rate_limits SET credit_ms=3600000');assert.equal((await py({action:'recover'})).outcome,'published');assert.ok((await pool.query('SELECT credit_ms FROM gregale_event_outbox_rate_limits')).rows[0].credit_ms<3600000);await pool.query('DELETE FROM gregale_event_outbox_rate_limits');
  await stage('attempt-budget',{retryPolicy:{initialBackoffMs:5,maxBackoffMs:300,maxAttempts:1}});await pool.query("UPDATE gregale_event_outbox SET attempts=1 WHERE producer_key='attempt-budget'");assert.equal((await py({action:'recover'})).status.last_code,'attempts_exhausted');assert.equal(posts.has('attempt-budget'),false);
  await stage('settlement-lost');let lostSettlement=false;
  const {outboxSQL}=await import('../dist/event-outbox-protocol.js');
  const uncertainPool={async connect(){const c=await outboxPool.connect();return {async query(sql,values){const result=await c.query(sql,values);if(sql===outboxSQL.settle&&!lostSettlement){lostSettlement=true;throw Error('lost acknowledged autocommit reply');}return result;},release:discard=>c.release(discard)};}};
  await assert.rejects(sdk.createEventOutbox(uncertainPool,{scope,token:'fixture-owner'}).recoverNext({timeoutMs:10000}),{code:'store_failed'});
  assert.equal((await py({action:'status',key:'settlement-lost'})).state,'published');assert.equal(posts.get('settlement-lost'),1);
  const wrongScope={...scope,account_id:randomUUID()};assert.deepEqual(await py({action:'status',key:'same-key',scope:wrongScope}),{});
  assert.equal((await py({action:'recover',scope:wrongScope})).outcome,'empty');
  const wrong=sdk.createEventOutbox(outboxPool,{scope:{...scope,account_id:randomUUID()},token:'fixture-owner'});assert.equal(await wrong.status('same-key',{timeoutMs:1000}),null);assert.equal((await wrong.recoverNext({timeoutMs:1000})).outcome,'empty');
  await stage('denied');const denied=sdk.createEventOutbox(outboxPool,{scope,token:'other'});assert.equal((await denied.recoverNext({timeoutMs:10000})).outcome,'blocked');assert.equal(native.has('denied'),false);
  assert.equal((await pool.query("SELECT count(*)::int AS n FROM gregale_event_outbox WHERE state='published' AND request_body IS NOT NULL")).rows[0].n,0);
});
