import assert from 'node:assert/strict';
import {inspect} from 'node:util';
import {test} from 'node:test';
import {createServer, get, type IncomingMessage} from 'node:http';
import {once} from 'node:events';
import {ObjectStorageBinding} from '../src/object-storage-runtime.js';

const values = () => ({ASSETS_ENDPOINT:'https://s3.gregale.test',ASSETS_REGION:'auto',ASSETS_BUCKET:'assets',ASSETS_ACCESS_KEY_ID:'private-access',ASSETS_SECRET_ACCESS_KEY:'private-secret',ASSETS_ADDRESSING_STYLE:'path'});

function deferred() {
  let resolve!: () => void;
  const promise = new Promise<void>(done => {resolve = done;});
  return {promise, resolve};
}

test('managed leases pin rotations, copies release once, and shutdown drains only after the last borrower', async () => {
  const retired: object[] = [];
  const b = new ObjectStorageBinding('ASSETS', config => ({key:config.credentials.secretAccessKey}), {retire: client => {retired.push(client);}});
  assert.throws(() => b.acquire());
  await b.reload(values());
  assert.throws(() => b.current(), /lease acquisition/);
  const first=b.acquire(), second=b.acquire(), old=first.current().client;
  assert.equal(second.current().client, old);
  assert.ok(!JSON.stringify(first).includes('private-'));
  assert.ok(!inspect(first).includes('private-'));
  await b.reload({...values(),ASSETS_SECRET_ACCESS_KEY:'private-new'});
  const current=b.acquire(), next=current.current();
  assert.equal(next.bucket,'assets');
  assert.equal(next.client.key,'private-new');
  first.release();
  assert.deepEqual(retired,[]);
  const copiedRelease=second.release;
  copiedRelease(); second.release();
  await b.waitForRetirement();
  assert.deepEqual(retired,[old]);
  assert.throws(() => second.current(),/released/);
  b.invalidate();
  assert.throws(() => b.acquire());
  assert.deepEqual(retired,[old]);
  current.release();
  await b.waitForRetirement();
  assert.deepEqual(retired,[old,next.client]);
});

test('drain cancellation preserves a lease and slow retirement permits new requests and invalidation', async () => {
  const entered=deferred(), finish=deferred();
  const retired: object[]=[];
  const b=new ObjectStorageBinding('ASSETS',config=>({key:config.credentials.secretAccessKey}),{retire:async client=>{
    if(client.key==='private-secret'){entered.resolve();await finish.promise;}
    retired.push(client);
  }});
  try {
    await b.reload(values());
    const lease=b.acquire(),old=lease.current().client;
    b.invalidate();
    await assert.rejects(b.waitForRetirement(AbortSignal.abort()),/cancelled/);
    assert.equal(lease.current().client,old);
    lease.release();
    await entered.promise;
    await b.reload({...values(),ASSETS_SECRET_ACCESS_KEY:'private-new'});
    const next=b.acquire(); next.release(); b.invalidate();
    const cancel=new AbortController();
    const draining=b.waitForRetirement(cancel.signal);
    cancel.abort();
    await assert.rejects(draining,/cancelled/);
    assert.ok(!retired.includes(old));
    finish.resolve();
    await b.waitForRetirement();
    assert.equal(retired.filter(client=>client===old).length,1);
  } finally {finish.resolve();b.invalidate();}
});

test('late rejected candidates are retired and queued factories are fenced; drains track construction', async () => {
  for(const cancelFactory of [false,true]){
    const entered=deferred(),finish=deferred();
    const retired: object[]=[];
    let calls=0;
    const candidate={key:'private-candidate'};
    const b=new ObjectStorageBinding('ASSETS',async()=>{calls++;entered.resolve();await finish.promise;return candidate;},{retire:client=>{retired.push(client);}});
    const cancel=new AbortController();
    const late=b.reload(values(),cancel.signal);
    const rejection=assert.rejects(late,/cancelled/);
    try{
      await entered.promise;
      const queuedRejection=cancelFactory ? Promise.resolve() : assert.rejects(b.reload(values()),/cancelled/);
      if(cancelFactory)cancel.abort(); else b.invalidate();
      const waitCancel=new AbortController();
      const draining=b.waitForRetirement(waitCancel.signal);
      waitCancel.abort();
      await assert.rejects(draining,/cancelled/);
      assert.deepEqual(retired,[]);
      finish.resolve();
      await Promise.all([rejection,queuedRejection]);
      await b.waitForRetirement();
      assert.equal(calls,1);
      assert.deepEqual(retired,[candidate]);
      assert.throws(()=>b.acquire());
    }finally{finish.resolve();}
  }
});

test('retirement failures and observers are sanitized, sticky and never retried', async () => {
  let count=0;
  const observed: Error[]=[];
  const b=new ObjectStorageBinding('ASSETS',()=>({key:'private-key'}),{retire:()=>{
    count++;throw new Error('private-driver');
  },onRetireError:async error=>{observed.push(error);throw new Error('private-observer');}});
  await b.reload(values());b.invalidate();
  for(let i=0;i<2;i++) await assert.rejects(b.waitForRetirement(),error=>error instanceof Error && !String(error).includes('private-'));
  assert.equal(count,1);assert.equal(observed.length,1);
  assert.ok(!String(observed[0]).includes('private-'));
});

test('managed factory cannot reuse an outstanding client or close its lease as a rejected alias',async()=>{
  const client={key:'private-shared'};
  let count=0;
  const b=new ObjectStorageBinding('ASSETS',()=>client,{retire:()=>{count++;}});
  await b.reload(values());
  const lease=b.acquire();
  await assert.rejects(b.reload(values()),/reused an owned client/);
  assert.equal(count,0);
  assert.equal(lease.current().client,client);
  lease.release();await b.waitForRetirement();
  assert.equal(count,1);
});

test('an actual HTTP response stream finishes across rotation and revocation before retirement',async()=>{
  const finish=deferred();
  const server=createServer((_req,res)=>{
    res.write('first-');
    void finish.promise.then(()=>res.end('last'));
  });
  server.listen(0,'127.0.0.1');
  await once(server,'listening');
  const address=server.address();
  assert.ok(address && typeof address!=='string');
  const b=new ObjectStorageBinding('ASSETS',()=>({body:undefined as IncomingMessage|undefined,closed:0}),{retire:client=>{client.closed++;client.body?.destroy();}});
  let body: IncomingMessage|undefined;
  try{
    await b.reload(values());
    const lease=b.acquire(),client=lease.current().client;
    try{
      body=await new Promise<IncomingMessage>((resolve,reject)=>{
        const request=get(`http://127.0.0.1:${address.port}`,resolve);
        request.setTimeout(3000,()=>request.destroy(new Error('owned stream fixture timed out')));
        request.on('error',reject);
      });
      client.body=body;
      const chunks: Buffer[]=[];
      const started=deferred();
      body.on('data',(chunk: Buffer)=>{chunks.push(chunk);started.resolve();});
      const ended=once(body,'end');
      await started.promise;
      await b.reload(values());b.invalidate();
      assert.equal(client.closed,0);
      finish.resolve();
      await ended;
      assert.equal(Buffer.concat(chunks).toString(),'first-last');
      body.destroy();
    }finally{body?.destroy();lease.release();}
    await b.waitForRetirement();
    assert.equal(client.closed,1);
  }finally{
    finish.resolve();body?.destroy();b.invalidate();
    server.closeAllConnections();
    await new Promise<void>(resolve=>server.close(()=>resolve()));
  }
});

test('atomic rotation, SDK option spreading, safe errors and revocation', async () => {
  const b = new ObjectStorageBinding('ASSETS', config => {
    assert.equal(config.forcePathStyle,true);
    assert.equal(config.requestChecksumCalculation,'WHEN_REQUIRED');
    assert.equal(config.responseChecksumValidation,'WHEN_REQUIRED');
    assert.equal(config.region,'auto');
    assert.equal(config.endpoint,'https://s3.gregale.test');
    assert.equal({...config}.credentials.accessKeyId,'private-access');
    assert.ok(!JSON.stringify(config).includes('private-'));
    assert.ok(!inspect(config).includes('private-'));
    if(config.credentials.secretAccessKey === 'private-rejected') throw new Error('private-driver-error');
    return {key:config.credentials.secretAccessKey};
  });
  assert.throws(()=>b.current());
  await b.reload(values());
  const old = b.current();
  await b.reload({...values(),ASSETS_SECRET_ACCESS_KEY:'private-rotated'});
  assert.equal(b.current().client.key,'private-rotated');
  assert.equal(b.current().bucket,'assets');
  assert.equal(old.client.key,'private-secret');
  await assert.rejects(b.reload({...values(),ASSETS_SECRET_ACCESS_KEY:'private-rejected'}), e=>e instanceof Error && !String(e).includes('private-'));
  assert.throws(()=>b.current());
  await assert.rejects(b.reload({...values(),ASSETS_SECRET_ACCESS_KEY:''}));
});

test('serialized candidates cannot overwrite a newer client; cancellation cannot apply',async()=>{
  let release!:()=>void;
  const hold = new Promise<void>(resolve=>{release=resolve;});
  const b = new ObjectStorageBinding('ASSETS',async config=>{
    if(config.credentials.secretAccessKey === 'private-secret') await hold;
    return config.credentials.secretAccessKey;
  });
  const input = values();
  const first=b.reload(input);
  input.ASSETS_SECRET_ACCESS_KEY='private-mutated';
  const second=b.reload({...values(),ASSETS_SECRET_ACCESS_KEY:'private-new'});
  release();await Promise.all([first,second]);
  assert.equal(b.current().client,'private-new');
  const aborted=AbortSignal.abort();
  await assert.rejects(b.reload(values(),aborted));
  assert.throws(()=>b.current());
});

test('invalid binding configuration cannot invoke a factory',async()=>{
  for(const [key,value] of [['ASSETS_ENDPOINT','https://user:private-secret@example.test'],['ASSETS_ENDPOINT','https://example.test?secret=x'],['ASSETS_BUCKET','../other'],['ASSETS_REGION','us east'],['ASSETS_ADDRESSING_STYLE','virtual']]){
    const b=new ObjectStorageBinding('ASSETS',()=>{throw new Error('factory should not run');});
    await assert.rejects(b.reload({...values(),[key!]:value!}),/configuration is invalid/);
  }
  assert.throws(()=>new ObjectStorageBinding('invalid-prefix',()=>null));
});

test('invalidation fences queued and late factories without retiring acquired clients', async () => {
  let release!: () => void;
  let entered!: () => void;
  const held = new Promise<void>(resolve => {release=resolve;});
  const started = new Promise<void>(resolve => {entered=resolve;});
  let calls=0;
  const b = new ObjectStorageBinding('ASSETS', async config => {
    calls++;
    if(config.credentials.secretAccessKey==='private-late') {entered(); await held;}
    return config.credentials.secretAccessKey;
  });
  await b.reload(values());
  const acquired=b.current();
  const late=b.reload({...values(),ASSETS_SECRET_ACCESS_KEY:'private-late'});
  await started;
  const queued=b.reload(values());
  b.invalidate();
  assert.throws(()=>b.current());
  release();
  await assert.rejects(late,/cancelled/);
  await assert.rejects(queued,/cancelled/);
  assert.equal(calls,2);
  assert.equal(acquired.client,'private-secret');
  assert.throws(()=>b.current());
  await b.reload({...values(),ASSETS_SECRET_ACCESS_KEY:'private-recovered'});
  assert.equal(b.current().client,'private-recovered');
});
