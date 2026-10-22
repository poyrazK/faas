import assert from 'node:assert/strict';
import {once} from 'node:events';
import {mkdtemp, readFile, rename, rm, stat, writeFile} from 'node:fs/promises';
import {createServer} from 'node:http';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {setTimeout as sleep} from 'node:timers/promises';
import {test, type TestContext} from 'node:test';
import {inspect} from 'node:util';
import {ObjectStorageBinding} from '../src/object-storage-runtime.js';
import {SecretReloadCoordinator, SecretReloadError, type SecretConsumer, type SecretReloadOptions} from '../src/secret-reload-runtime.js';

const revision = (key: string): string => key.repeat(64);
const generation = 'e'.repeat(32);
const values = (key = 'private-first') => ({
  ASSETS_ENDPOINT: 'https://storage.gregale.test', ASSETS_REGION: 'auto', ASSETS_BUCKET: 'assets',
  ASSETS_ACCESS_KEY_ID: 'private-access', ASSETS_SECRET_ACCESS_KEY: key, ASSETS_ADDRESSING_STYLE: 'path',
});
const envelope = (key = 'a', secrets: unknown = values()): string => JSON.stringify({revision: revision(key), secrets});
const environment = () => ({FAAS_SECRETS_SNAPSHOT_FILE: '/snapshot', FAAS_SECRETS_RELOAD_READY_FILE: '/ready',
  FAAS_SECRETS_RELOAD_ACK_ENDPOINT: 'http://metadata.test/ack?workload=worker', FAAS_SECRETS_RELOAD_GENERATION: generation});
const deferred = <T = void>() => {
  let resolve!: (value: T | PromiseLike<T>) => void;
  const promise = new Promise<T>(done => {resolve=done;});
  return {promise, resolve};
};
const nextTurn = (): Promise<void> => new Promise(resolve => setImmediate(resolve));

function fixture(t: TestContext, options: Partial<SecretReloadOptions> = {}) {
  let raw = envelope();
  const receipts: Array<{revision: string; status: string; generation: string}> = [];
  const storage = new ObjectStorageBinding('ASSETS', config => ({key: config.credentials.secretAccessKey}));
  const coordinator = new SecretReloadCoordinator({
    consumers: [storage.secretConsumer()], env: environment(), ...options,
    io: {read: async () => raw, ready: async () => {}, sleep: async () => {},
      fetch: async (_input, init) => {receipts.push(JSON.parse(String(init?.body))); return new Response(null, {status:202});},
      ...options.io},
  });
  t.after(() => coordinator.stop());
  return {coordinator, storage, receipts, publish: (value: string) => {raw=value;}};
}

test('managed leases survive partial adoption failure and coordinator shutdown', async t => {
  const retired: string[]=[];
  let fail=false;
  const storage=new ObjectStorageBinding('ASSETS',c=>({key:c.credentials.secretAccessKey}),{retire:client=>{retired.push(client.key);}});
  const {coordinator,receipts,publish}=fixture(t,{consumers:[storage.secretConsumer(),{
    apply:()=>{if(fail)throw new Error('private-other-consumer');},invalidate:()=>{},
  }]});
  await coordinator.start();
  const old=storage.acquire();
  try{
    fail=true;publish(envelope('b',values('private-failed')));
    await assert.rejects(coordinator.reload(),SecretReloadError);
    assert.throws(()=>storage.acquire());
    await nextTurn();
    assert.deepEqual(retired,['private-failed']);
    assert.equal(old.current().client.key,'private-first');
    assert.deepEqual(receipts.map(r=>r.status),['applied','failed']);
    fail=false;publish(envelope('c',values('private-recovered')));
    await coordinator.reload();
    const current=storage.acquire();
    try{
      coordinator.stop();
      assert.throws(()=>storage.acquire());
      assert.deepEqual(retired,['private-failed']);
      old.release();current.release();
      await storage.waitForRetirement();
      assert.deepEqual(retired.sort(),['private-failed','private-first','private-recovered']);
    }finally{current.release();}
  }finally{old.release();coordinator.stop();}
});

test('manual refresh cancellation fences adoption and permits a later refresh', {timeout: 3000}, async t => {
  const entered = deferred(), finish = deferred(); let block = false;
  const storage = new ObjectStorageBinding('ASSETS', config => config.credentials.secretAccessKey);
  const {coordinator, receipts, publish} = fixture(t, {consumers: [storage.secretConsumer(), {
    apply: async () => {if (block) {entered.resolve(); await finish.promise;}}, invalidate: () => {},
  }]});
  await coordinator.start();
  block = true; publish(envelope('b', values('private-second')));
  const cancel = new AbortController();
  const refresh = coordinator.reload(cancel.signal); await entered.promise;
  cancel.abort(); await assert.rejects(refresh, /cancelled/);
  assert.throws(() => storage.current()); assert.equal(receipts.length, 1);
  finish.resolve(); await nextTurn();
  block = false; publish(envelope('c', values('private-recovered')));
  await coordinator.reload();
  assert.equal(storage.current().client, 'private-recovered');
  assert.deepEqual(receipts.map(r => r.revision), [revision('a'), revision('c')]);
});

test('a queued cancelled refresh returns promptly without running or invalidating another refresh', {timeout: 3000}, async t => {
  const entered = deferred(), finish = deferred(); let reads = 0;
  const {coordinator, storage, receipts} = fixture(t, {io: {read: async () => {
    if (++reads === 2) {entered.resolve(); await finish.promise;}
    return envelope();
  }}});
  await coordinator.start();
  const first = coordinator.reload(); await entered.promise;
  const cancel = new AbortController(), queued = coordinator.reload(cancel.signal);
  cancel.abort(); await assert.rejects(queued, /cancelled/);
  finish.resolve(); await first; await nextTurn();
  assert.equal(reads, 2); assert.equal(receipts.length, 2); assert.ok(storage.current());
  await assert.rejects(coordinator.reload(AbortSignal.abort()), /cancelled/);
  assert.ok(storage.current());
});

test('startup registers SIGHUP before readiness, waits for a version and fences exact ACKs after every consumer', async t => {
  const applied: string[] = [];
  let ready = false, reads = 0;
  const baseline=process.listenerCount('SIGHUP');
  const last=deferred(), entered=deferred();
  const storage=new ObjectStorageBinding('ASSETS', config=>config.credentials.secretAccessKey);
  const {coordinator, receipts}=fixture(t,{consumers:[storage.secretConsumer(),{
    apply: async snapshot=>{
      assert.ok(Object.isFrozen(snapshot)); assert.ok(Object.isFrozen(snapshot.secrets));
      assert.ok(!JSON.stringify(snapshot).includes('private-')); assert.ok(!inspect(snapshot).includes('private-'));
      applied.push(snapshot.revision); entered.resolve(); await last.promise;
    }, invalidate: ()=>{},
  }], io:{
    ready:async ()=>{assert.equal(process.listenerCount('SIGHUP'),baseline+1);ready=true;},
    read:async ()=>{assert.ok(ready);return reads++ === 0 ? JSON.stringify({revision:'',secrets:{}}) : envelope();},
  }});
  const startup=coordinator.start();
  assert.equal(coordinator.start(),startup);
  await entered.promise;
  assert.equal(receipts.length,0);
  last.resolve();
  assert.equal(await startup,revision('a'));
  assert.deepEqual(receipts,[{revision:revision('a'),status:'applied',generation}]);
  assert.equal(storage.current().client,'private-first');
  assert.deepEqual(applied,[revision('a')]);
  coordinator.stop(); assert.equal(process.listenerCount('SIGHUP'),baseline);
  await assert.rejects(coordinator.reload()); await assert.rejects(coordinator.start());
});

test('rotation and revocation disable acquisition while already acquired requests can finish', async t => {
  const {coordinator, storage, receipts, publish}=fixture(t);
  await coordinator.start();
  const acquired=storage.current();
  publish(envelope('b',values('private-rotated')));
  await coordinator.reload();
  assert.equal(storage.current().client.key,'private-rotated');
  assert.equal(acquired.client.key,'private-first');
  publish(envelope('c',{}));
  await assert.rejects(coordinator.reload(),SecretReloadError);
  assert.throws(()=>storage.current());
  assert.deepEqual(receipts[2],{revision:revision('c'),status:'failed',generation});
  publish(envelope('d',values('private-recovered')));
  await coordinator.reload(); assert.equal(storage.current().client.key,'private-recovered');
  assert.ok(!JSON.stringify(receipts).includes('private-'));
});

test('a later consumer failure disables every binding and never ACKs partial adoption', async t => {
  let fail=false, invalidations=0;
  const one=new ObjectStorageBinding('ASSETS',c=>c.credentials.secretAccessKey);
  const two=new ObjectStorageBinding('ASSETS',c=>c.credentials.secretAccessKey);
  const {coordinator,receipts,publish}=fixture(t,{consumers:[one.secretConsumer(),two.secretConsumer(),{
    apply:()=>{if(fail) throw new Error('private-driver-error');}, invalidate:()=>{invalidations++;},
  }]});
  await coordinator.start(); fail=true;publish(envelope('b',values('private-second')));
  await assert.rejects(coordinator.reload(),error=>error instanceof SecretReloadError && !String(error).includes('private-'));
  assert.throws(()=>one.current());assert.throws(()=>two.current());assert.ok(invalidations>=3);
  assert.deepEqual(receipts.map(r=>r.status),['applied','failed']);
});

test('consumer invalidation exceptions do not prevent other consumers from being disabled or fabricate ACKs', async t => {
  let invalidated=false, applied=false;
  const {coordinator,receipts}=fixture(t,{consumers:[
    {apply:()=>{},invalidate:()=>{throw new Error('private-error');}},
    {apply:()=>{applied=true;},invalidate:()=>{invalidated=true;}},
  ]});
  await assert.rejects(coordinator.start(),SecretReloadError);
  assert.equal(invalidated,true);assert.equal(applied,false);assert.equal(receipts.length,0);
});

test('asynchronous invalidation cannot qualify adoption or leak rejected callback errors', async t => {
  let applied=false;
  const {coordinator,receipts}=fixture(t,{consumers:[{
    apply:()=>{applied=true;},invalidate:async()=>{throw new Error('private-invalidation-error');},
  }]});
  await assert.rejects(coordinator.start(),SecretReloadError);await nextTurn();
  assert.equal(applied,false);assert.equal(receipts.length,0);
});

test('stale applied and failed ACKs reread authoritative snapshots and never adopt legacy files', async t => {
  const applied:string[]=[], receipts:unknown[]=[];
  let current=envelope(), staleFailures=0;
  const {coordinator}=fixture(t,{consumers:[{apply:s=>{
    applied.push(s.revision);if(s.revision===revision('b')) throw new Error('private-error');
  },invalidate:()=>{}}],io:{
    read:async path=>{assert.equal(path,'/snapshot');return current;},
    fetch:async(_input,init)=>{
      const body=JSON.parse(String(init?.body));receipts.push(body);
      if(body.revision===revision('a')) {current=envelope('b');return new Response(null,{status:409});}
      if(body.revision===revision('b')) {staleFailures++;current=envelope('c');return new Response(null,{status:409});}
      return new Response(null,{status:202});
    },
  }});
  assert.equal(await coordinator.start(),revision('c'));assert.equal(staleFailures,1);
  assert.deepEqual(applied,['a','b','c'].map(revision));
  assert.deepEqual(receipts,['applied','failed','applied'].map((status,i)=>({revision:revision(['a','b','c'][i]!),status,generation})));
});

test('stale ACK exhaustion disables adopted clients and completes a bounded attempt', async t => {
  let calls=0;
  const storage=new ObjectStorageBinding('ASSETS',c=>c.credentials.secretAccessKey);
  const {coordinator}=fixture(t,{consumers:[storage.secretConsumer()],io:{fetch:async()=>{calls++;return new Response(null,{status:409});}}});
  await assert.rejects(coordinator.start(),SecretReloadError);
  assert.equal(calls,5);assert.throws(()=>storage.current());
});

test('a stale ACK disables the old client before waiting on the next snapshot', async t => {
  const reading=deferred(),hold=deferred<string>();let reads=0;
  const storage=new ObjectStorageBinding('ASSETS',()=>({}));
  const {coordinator}=fixture(t,{consumers:[storage.secretConsumer()],io:{
    read:async()=>{if(reads++===0)return envelope();reading.resolve();return hold.promise;},
    fetch:async()=>new Response(null,{status:409}),
  }});
  const startup=coordinator.start();await reading.promise;
  assert.throws(()=>storage.current());coordinator.stop();await assert.rejects(startup,/cancelled/);
  hold.resolve(envelope('b'));
});

test('transport failures and 503 retry the identical closed body with captured identity', async t => {
  const env=environment(), bodies:string[]=[], endpoints:string[]=[];
  const {coordinator}=fixture(t,{env,io:{fetch:async(input,init)=>{
    bodies.push(String(init?.body));endpoints.push(String(input));
    assert.equal(init?.redirect,'error');
    env.FAAS_SECRETS_RELOAD_GENERATION='f'.repeat(32);
    env.FAAS_SECRETS_RELOAD_ACK_ENDPOINT='http://different.test/ack';
    if(bodies.length===1) throw new Error('private-transport-error');
    return new Response(null,{status:bodies.length===2 ? 503 : 202});
  }}});
  await coordinator.start();await coordinator.reload();
  assert.equal(bodies.length,4);assert.equal(new Set(bodies).size,1);
  assert.deepEqual(JSON.parse(bodies[0]!),{revision:revision('a'),status:'applied',generation});
  assert.equal(new Set(endpoints).size,1);assert.equal(endpoints[0],'http://metadata.test/ack?workload=worker');
});

test('unconfirmed ACKs fail closed; terminal HTTP responses are not retried', async t => {
  for(const code of [400,401,302,500,503]) {
    let calls=0;
    const storage=new ObjectStorageBinding('ASSETS',()=>({}));
    const {coordinator}=fixture(t,{consumers:[storage.secretConsumer()],io:{fetch:async()=>{calls++;return new Response(null,{status:code});}}});
    await assert.rejects(coordinator.start(),SecretReloadError);
    assert.equal(calls,code===503?10:1);assert.throws(()=>storage.current());
  }
});

test('authoritative snapshot ENOENT is retried, malformed snapshots receive no ACK and disable existing clients', async t => {
  let reads=0;
  const {coordinator,storage,receipts}=fixture(t,{io:{read:async()=>{
    if(reads++<2) throw Object.assign(new Error('private-path'),{code:'ENOENT'});
    return envelope();
  }}});
  await coordinator.start();assert.equal(reads,3);assert.equal(receipts.length,1);
  coordinator.stop();
  // Each malformed projection is authoritative; no separate-file fallback.
  for(const raw of ['{private-broken', 'null', '[]', JSON.stringify({revision:revision('a'),secrets:{token:12}}),
    JSON.stringify({revision:'private-invalid',secrets:values()}),JSON.stringify({revision:'',secrets:values()})]) {
    const f=fixture(t);await f.coordinator.start();f.publish(raw);
    await assert.rejects(f.coordinator.reload(),error=>error instanceof SecretReloadError && !String(error).includes('private-'));
    assert.equal(f.receipts.length,1);assert.throws(()=>f.storage.current());f.coordinator.stop();
  }
  assert.throws(()=>storage.current());
});

test('initial wait timeout and missing readiness fail without applying or acknowledging', async t => {
  let applied=false;
  const keepAlive=setTimeout(()=>{},1000);t.after(()=>clearTimeout(keepAlive));
  const {coordinator,receipts}=fixture(t,{startupTimeoutMs:15,consumers:[{apply:()=>{applied=true;},invalidate:()=>{}}],io:{
    read:async()=>JSON.stringify({revision:'',secrets:{}}),sleep:(ms,signal)=>sleep(ms,undefined,{signal}),
  }});
  await assert.rejects(coordinator.start(),SecretReloadError);assert.equal(applied,false);assert.equal(receipts.length,0);
  const broken=fixture(t,{io:{ready:async()=>{throw new Error('private-file-path');}}});
  await assert.rejects(broken.coordinator.start(),SecretReloadError);assert.equal(broken.receipts.length,0);
});

test('concurrent refreshes and signal bursts serialize application work and preserve one follow-up refresh', async t => {
  let active=0,maximum=0,calls=0;
  const hold=deferred(),entered=deferred(),done=deferred();
  const {coordinator}=fixture(t,{consumers:[{apply:async()=>{
    active++;maximum=Math.max(maximum,active);calls++;
    if(calls===2) {entered.resolve();await hold.promise;}
    if(calls===5) done.resolve();
    active--;
  },invalidate:()=>{}}]});
  await coordinator.start();
  const manual=coordinator.reload();await entered.promise;
  process.emit('SIGHUP');
  for(let i=0;i<100;i++) process.emit('SIGHUP');
  const other=coordinator.reload();hold.resolve();
  await Promise.all([manual,other,done.promise]);await nextTurn();
  assert.equal(maximum,1);assert.equal(calls,5); // startup, two manuals, two coalesced signal refreshes
});

test('signals during bootstrap queue behind it; signal errors are sanitized and later reloads recover', async t => {
  const first=deferred(),entered=deferred(),errorSeen=deferred<SecretReloadError>();
  let calls=0,fail=false;
  const {coordinator,receipts}=fixture(t,{onError:error=>errorSeen.resolve(error),consumers:[{
    apply:async()=>{calls++;if(calls===1){entered.resolve();await first.promise;}if(fail)throw new Error('private-driver');},invalidate:()=>{},
  }]});
  const startup=coordinator.start();await entered.promise;
  process.emit('SIGHUP');await nextTurn();assert.equal(calls,1);assert.equal(receipts.length,0);
  first.resolve();await startup;await nextTurn();assert.equal(calls,2);
  fail=true;process.emit('SIGHUP');
  const error=await errorSeen.promise;assert.ok(error instanceof SecretReloadError);assert.ok(!String(error).includes('private-'));
  fail=false;await coordinator.reload();assert.equal(receipts.at(-1)?.status,'applied');
});

test('shutdown aborts ignored cancellation promptly, fences late factories and never ACKs or overlaps queued work', async t => {
  const shutdown=new AbortController(),entered=deferred(),hold=deferred();
  let calls=0;
  const storage=new ObjectStorageBinding('ASSETS',async()=>{calls++;if(calls===2){entered.resolve();await hold.promise;}return {};});
  const {coordinator,receipts}=fixture(t,{signal:shutdown.signal,consumers:[storage.secretConsumer()]});
  await coordinator.start();const reload=coordinator.reload();await entered.promise;
  const queued=coordinator.reload();shutdown.abort();
  await assert.rejects(reload,/cancelled/);await assert.rejects(queued,/cancelled/);
  assert.equal(receipts.length,1);assert.throws(()=>storage.current());
  hold.resolve();await nextTurn();assert.throws(()=>storage.current());assert.equal(calls,2);
  await assert.rejects(coordinator.start());await assert.rejects(coordinator.reload());
});

test('shutdown during a hung read or ACK prevents retry and future callbacks', async t => {
  for(const phase of ['read','ack']) {
    const entered=deferred(),hold=deferred<string>();let applied=0,acks=0;
    const {coordinator}=fixture(t,{consumers:[{apply:()=>{applied++;},invalidate:()=>{}}],io:{
      read:async()=>{if(phase==='read'){entered.resolve();return hold.promise;}return envelope();},
      fetch:async()=>{acks++;entered.resolve();await hold.promise;return new Response(null,{status:202});},
    }});
    const startup=coordinator.start();await entered.promise;coordinator.stop();
    await assert.rejects(startup,/cancelled/);hold.resolve(envelope());await nextTurn();
    assert.equal(applied,phase==='read'?0:1);assert.equal(acks,phase==='read'?0:1);
  }
});

test('invalid or legacy-only contracts cannot register a signal handler or borrow a missing generation', async () => {
  const consumer:SecretConsumer={apply:()=>{},invalidate:()=>{}};
  const env:Record<string,string|undefined>=environment();
  const baseline=process.listenerCount('SIGHUP');
  for(const [key,value] of [['FAAS_SECRETS_RELOAD_GENERATION',undefined],['FAAS_SECRETS_RELOAD_GENERATION','bad'],
    ['FAAS_SECRETS_SNAPSHOT_FILE',undefined],['FAAS_SECRETS_RELOAD_READY_FILE',''],
    ['FAAS_SECRETS_SNAPSHOT_FILE','relative'],['FAAS_SECRETS_RELOAD_ACK_ENDPOINT','https://user:private@host.test/ack']]) {
    assert.throws(()=>new SecretReloadCoordinator({consumers:[consumer],env:{...env,[key!]:value}}),SecretReloadError);
  }
  assert.throws(()=>new SecretReloadCoordinator({consumers:[],env}),SecretReloadError);
  assert.throws(()=>new SecretReloadCoordinator({consumers:[consumer],env,startupTimeoutMs:0}),SecretReloadError);
  assert.equal(process.listenerCount('SIGHUP'),baseline);
});

test('real atomic files and metadata HTTP transport qualify rotation, stale retries and revocation', async t => {
  const previousFetch=globalThis.fetch;
  globalThis.fetch=async()=>{throw new Error('private-global-wrapper-must-not-run');};
  t.after(()=>{globalThis.fetch=previousFetch;});
  const directory=await mkdtemp(join(tmpdir(),'gregale-secret-coordinator-'));
  t.after(()=>rm(directory,{recursive:true,force:true}));
  const snapshot=join(directory,'snapshot.json'),ready=join(directory,'ready');
  const publish=async(key:string,secrets:unknown):Promise<void>=>{
    const candidate=join(directory,'next.json');await writeFile(candidate,envelope(key,secrets),{mode:0o600});await rename(candidate,snapshot);
  };
  await publish('a',values());
  const storage=new ObjectStorageBinding('ASSETS',config=>config.credentials.secretAccessKey);
  const receipts:unknown[]=[],seen=deferred();let rotateAtAck=true;
  const server=createServer((request,response)=>{
    void (async()=>{
      assert.equal(request.method,'POST');assert.equal(request.url,'/ack?workload=worker');
      assert.equal(request.headers.authorization,undefined);assert.equal(request.headers['idempotency-key'],undefined);
      let raw='';for await(const chunk of request)raw+=String(chunk);
      const body=JSON.parse(raw);receipts.push(body);
      assert.equal(body.generation,generation);assert.deepEqual(Object.keys(body).sort(),['generation','revision','status']);
      if(rotateAtAck){rotateAtAck=false;await publish('b',values('private-next'));response.writeHead(409,{'content-type':'application/problem+json'}).end('{"detail":"private-metadata-error"}');return;}
      if(body.status==='failed')assert.throws(()=>storage.current());
      else assert.equal(storage.current().client,body.revision===revision('b')?'private-next':'private-final');
      response.writeHead(202).end();seen.resolve();
    })().catch(()=>{response.writeHead(500).end();});
  });
  server.listen(0,'127.0.0.1');await once(server,'listening');
  t.after(()=>new Promise<void>((resolve,reject)=>server.close(error=>error?reject(error):resolve())));
  const address=server.address();assert.ok(address&&typeof address!=='string');
  const coordinator=new SecretReloadCoordinator({consumers:[storage.secretConsumer()],env:{...environment(),
    FAAS_SECRETS_SNAPSHOT_FILE:snapshot,FAAS_SECRETS_RELOAD_READY_FILE:ready,
    FAAS_SECRETS_RELOAD_ACK_ENDPOINT:`http://127.0.0.1:${address.port}/ack?workload=worker`}});
  t.after(()=>coordinator.stop());
  assert.equal(await coordinator.start(),revision('b'));await seen.promise;
  assert.equal(await readFile(ready,'utf8'),'ready\n');assert.equal((await stat(ready)).mode&0o777,0o600);
  await publish('c',{});await assert.rejects(coordinator.reload(),SecretReloadError);assert.throws(()=>storage.current());
  await publish('d',values('private-final'));await coordinator.reload();assert.equal(storage.current().client,'private-final');
  assert.deepEqual(receipts,['applied','applied','failed','applied'].map((status,i)=>({revision:revision(['a','b','c','d'][i]!),status,generation})));
});
