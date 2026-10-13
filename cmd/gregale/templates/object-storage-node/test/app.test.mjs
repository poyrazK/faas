import assert from 'node:assert/strict';
import {once} from 'node:events';
import {mkdtemp, readdir, rm} from 'node:fs/promises';
import {get, request} from 'node:http';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {PassThrough, Readable} from 'node:stream';
import {test} from 'node:test';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {createStorageApp, limits} from '../app.mjs';

const token='a'.repeat(48);
const settings=()=>({GREGALE_S3_ASSETS_ENDPOINT:'https://gateway.local.test',GREGALE_S3_ASSETS_REGION:'auto',
  GREGALE_S3_ASSETS_BUCKET:'assets',GREGALE_S3_ASSETS_ACCESS_KEY_ID:'private-access',
  GREGALE_S3_ASSETS_SECRET_ACCESS_KEY:'private-secret',GREGALE_S3_ASSETS_ADDRESSING_STYLE:'path',STORAGE_APP_TOKEN:token});

test('missing guest contract exits with one sanitized startup event',async()=>{
  await assert.rejects(promisify(execFile)(process.execPath,['server.mjs'],{cwd:new URL('..',import.meta.url),env:{},timeout:3000}),
    error=>error.code===1 && error.stderr==='{"code":"startup_unavailable"}\n' && error.stdout==='');
});

async function fixture(t) {
  const directory=await mkdtemp(join(tmpdir(),'gregale-storage-starter-'));
  const objects=new Map(), clients=[], bodies=[], logs=[], receipts=[];
  let raw=JSON.stringify({revision:'a'.repeat(64),secrets:settings()});
  let listingFailure=false;
  const app=createStorageApp({spoolRoot:directory,env:{FAAS_SECRETS_SNAPSHOT_FILE:'/snapshot',FAAS_SECRETS_RELOAD_READY_FILE:'/ready',
    FAAS_SECRETS_RELOAD_ACK_ENDPOINT:'http://metadata.local.test/ack',FAAS_SECRETS_RELOAD_GENERATION:'e'.repeat(32)},
  io:{read:async()=>raw,ready:async()=>{},sleep:async()=>{},fetch:async(_url,options)=>{
    receipts.push(JSON.parse(options.body));return new Response(null,{status:202});
  }},log:code=>logs.push(code),clientFactory:config=>{
    assert.equal(config.forcePathStyle,true);
    assert.equal(config.requestChecksumCalculation,'WHEN_REQUIRED');
    assert.equal(config.credentials.accessKeyId,'private-access');
    const client={key:config.credentials.secretAccessKey,destroyed:0,destroy(){this.destroyed++;},async send(command,{abortSignal}) {
      abortSignal.throwIfAborted();
      if(command.constructor.name==='ListObjectsV2Command') {
        assert.equal(command.input.MaxKeys,1);
        if(listingFailure)throw new Error('private-provider-error');
        return {};
      }
      if(command.constructor.name==='PutObjectCommand'){
        const chunks=[];for await(const chunk of command.input.Body)chunks.push(chunk);
        const bytes=Buffer.concat(chunks);
        assert.equal(bytes.length,command.input.ContentLength);
        objects.set(command.input.Key,bytes);
        return {};
      }
      if(command.input.Key==='missing') {const error=new Error('private-provider');error.name='NoSuchKey';throw error;}
      const body=command.input.Key.startsWith('slow')?new PassThrough():Readable.from([objects.get(command.input.Key)]);
      bodies.push(body);
      if(command.input.Key.startsWith('slow')){body.write('first-');return {Body:body,ContentLength:10};}
      return {Body:body,ContentLength:objects.get(command.input.Key).length};
    }};
    clients.push(client);return client;
  }});
  t.after(async()=>{for(const body of bodies)body.destroy();await app.stop(1000);await rm(directory,{recursive:true,force:true});});
  const address=await app.start(0,'127.0.0.1');
  const base=`http://127.0.0.1:${address.port}`;
  return {app,base,objects,clients,bodies,logs,receipts,directory,
    publish(revision,secrets=settings()){raw=JSON.stringify({revision:revision.repeat(64),secrets});},
    failListing(){listingFailure=true;},recoverListing(){listingFailure=false;}};
}

function streaming(base,key) {
  return new Promise((resolve,reject)=>{
    const req=get(base+'/objects/'+key,{headers:{authorization:'Bearer '+token}},res=>{
      const chunks=[];res.on('data',chunk=>chunks.push(chunk));
      const started=once(res,'data');
      const ended=once(res,'end').then(()=>Buffer.concat(chunks));
      void ended.catch(()=>{});
      resolve({req,res,started,ended});
    });
    req.on('error',reject);
  });
}

test('authenticated byte-exact transfers, attachment headers and sanitized failures',async t=>{
  const f=await fixture(t);
  const data=Buffer.from([0,255,37,13,10,1,2]);
  assert.equal((await fetch(f.base+'/readyz')).status,200);
  assert.equal((await fetch(f.base+'/objects/file.bin')).status,401);
  const headers={authorization:'Bearer '+token,'content-type':'text/html'};
  assert.equal((await fetch(f.base+'/objects/file.bin',{method:'PUT',headers,body:data})).status,201);
  const response=await fetch(f.base+'/objects/file.bin',{headers});
  assert.equal(response.status,200);
  assert.equal(response.headers.get('content-disposition'),'attachment');
  assert.equal(response.headers.get('content-type'),'application/octet-stream');
  assert.deepEqual(Buffer.from(await response.arrayBuffer()),data);
  assert.equal((await fetch(f.base+'/objects/missing',{headers})).status,404);
  const error=await fetch(f.base+'/objects/bad%2Fkey',{headers});
  assert.equal(error.status,400);
  assert.ok(!JSON.stringify(f.logs).includes('private-'));
  assert.deepEqual(await readdir(f.directory),[]);
});

test('rotation preserves an admitted response stream, revocation blocks acquisition, recovery validates access',async t=>{
  const f=await fixture(t);
  const held=await streaming(f.base,'slow.bin');
  await held.started;
  f.publish('b',{...settings(),GREGALE_S3_ASSETS_SECRET_ACCESS_KEY:'private-rotated'});
  await f.app.coordinator.reload();
  assert.equal(f.clients[0].destroyed,0);
  f.bodies[0].end('last');
  assert.equal((await held.ended).toString(),'first-last');
  await f.app.storage.waitForRetirement();
  assert.equal(f.clients[0].destroyed,1);
  f.failListing();f.publish('c');
  await assert.rejects(f.app.coordinator.reload());
  assert.equal((await fetch(f.base+'/readyz')).status,503);
  assert.equal((await fetch(f.base+'/objects/slow.bin',{headers:{authorization:'Bearer '+token}})).status,503);
  assert.equal(f.clients[2].destroyed,1); // Candidate readiness failed before publication.
  assert.deepEqual(f.receipts.map(r=>r.status),['applied','applied','failed']);
  f.recoverListing();f.publish('d');
  await f.app.coordinator.reload();
  assert.equal((await fetch(f.base+'/readyz')).status,200);
  assert.ok(!JSON.stringify(f.logs).includes('private-'));
});

test('graceful shutdown keeps the stream leased until the last byte is forwarded',async t=>{
  const f=await fixture(t);
  const held=await streaming(f.base,'slow.bin');
  await held.started;
  const stop=f.app.stop(1500);
  assert.equal(f.app.ready(),false);
  assert.equal(f.clients[0].destroyed,0);
  f.bodies[0].end('last');
  assert.equal((await held.ended).toString(),'first-last');
  assert.equal(await stop,true);
  assert.equal(f.clients[0].destroyed,1);
});

test('shutdown deadline cancels outstanding bodies and releases the retired client',async t=>{
  const f=await fixture(t);
  const held=await streaming(f.base,'slow.bin');
  await held.started;
  assert.equal(await f.app.stop(20),false);
  await f.app.storage.waitForRetirement(AbortSignal.timeout(1000));
  assert.ok(f.bodies[0].destroyed);
  assert.equal(f.clients[0].destroyed,1);
  assert.deepEqual(f.logs,['shutdown_timeout','storage_unavailable']);
});

test('disconnected download closes its body; disconnected upload leaves no staging files or provider write',async t=>{
  const f=await fixture(t);
  const held=await streaming(f.base,'slow.bin');
  await held.started;
  const bodyClosed=new Promise(resolve=>f.bodies[0].once('close',resolve));
  held.res.destroy();held.req.destroy();
  await bodyClosed;
  const upload=request(f.base+'/objects/partial.bin',{method:'PUT',headers:{authorization:'Bearer '+token}});
  upload.on('error',()=>{});
  upload.write('partial');
  // Wait for staging admission, then cut the input before EOF.
  const deadline=Date.now()+3000;
  while((await readdir(f.directory)).length===0 && Date.now()<deadline)await new Promise(resolve=>setTimeout(resolve,5));
  assert.equal((await readdir(f.directory)).length,1);
  upload.destroy();
  assert.equal(await f.app.stop(1000),true);
  assert.deepEqual(await readdir(f.directory),[]);
  assert.equal(f.objects.has('partial.bin'),false);
});

test('declared oversized uploads are rejected before staging and readiness rejects invalid auth adoption',async t=>{
  const f=await fixture(t);
  const status=await new Promise((resolve,reject)=>{
    const req=request(f.base+'/objects/large.bin',{method:'PUT',headers:{authorization:'Bearer '+token,'content-length':String(limits.uploadBytes+1)}},
      res=>{res.resume();resolve(res.statusCode);req.destroy();});
    req.on('error',reject);req.flushHeaders();
  });
  assert.equal(status,413);
  assert.deepEqual(await readdir(f.directory),[]);
  f.publish('b',{...settings(),STORAGE_APP_TOKEN:'too-short'});
  await assert.rejects(f.app.coordinator.reload());
  assert.equal(f.app.ready(),false);
  await f.app.storage.waitForRetirement();
  assert.equal(f.clients[0].destroyed,1);
  assert.equal(f.clients[1].destroyed,1);
});

test('upload admission is bounded and application authentication rotates with the snapshot',async t=>{
  const f=await fixture(t);
  const uploads=[];
  for(let i=0;i<limits.uploads;i++) {
    const req=request(f.base+'/objects/held-'+i,{method:'PUT',headers:{authorization:'Bearer '+token}});
    req.on('error',()=>{});req.write('partial');uploads.push(req);
  }
  const deadline=Date.now()+3000;
  while((await readdir(f.directory)).length<limits.uploads && Date.now()<deadline)await new Promise(resolve=>setTimeout(resolve,5));
  assert.equal((await readdir(f.directory)).length,limits.uploads);
  assert.equal((await fetch(f.base+'/objects/excess',{method:'PUT',headers:{authorization:'Bearer '+token},body:'blocked'})).status,503);
  assert.equal(f.objects.has('excess'),false);
  for(const req of uploads)req.destroy();
  const rotated='b'.repeat(48);
  f.publish('b',{...settings(),STORAGE_APP_TOKEN:rotated});
  await f.app.coordinator.reload();
  assert.equal((await fetch(f.base+'/objects/missing',{headers:{authorization:'Bearer '+token}})).status,401);
  assert.equal((await fetch(f.base+'/objects/missing',{headers:{authorization:'Bearer '+rotated}})).status,404);
  assert.equal(await f.app.stop(1000),true);
  assert.deepEqual(await readdir(f.directory),[]);
});
