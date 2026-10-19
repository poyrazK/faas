import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {once} from 'node:events';
import {cp, mkdir, mkdtemp, readFile, rename, rm, writeFile} from 'node:fs/promises';
import {createServer} from 'node:http';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {test} from 'node:test';
import {pathToFileURL} from 'node:url';

test('vendored storage runtime imports and drains from an isolated starter directory', async t => {
  const directory=await mkdtemp(join(tmpdir(),'gregale-storage-runtime-'));
  t.after(()=>rm(directory,{recursive:true,force:true}));
  await cp(new URL('../../../cmd/gregale/templates/object-storage-node/runtime/',import.meta.url),directory,{recursive:true});
  await writeFile(join(directory,'package.json'),JSON.stringify({type:'module'}));
  const {ObjectStorageBinding}=await import(pathToFileURL(join(directory,'object-storage-runtime.js')).href);
  let retired=0;
  const binding=new ObjectStorageBinding('ASSETS',()=>({}),{retire:()=>{retired++;}});
  await binding.reload({ASSETS_ENDPOINT:'https://storage.example',ASSETS_REGION:'auto',ASSETS_BUCKET:'assets',
    ASSETS_ADDRESSING_STYLE:'path',ASSETS_ACCESS_KEY_ID:'fixture-access',ASSETS_SECRET_ACCESS_KEY:'fixture-key'});
  const lease=binding.acquire();
  binding.invalidate();
  assert.equal(retired,0);
  lease.release();
  await binding.waitForRetirement();
  assert.equal(retired,1);
});

test('PostgreSQL starter adapters import and drain using only their vendored runtime', async t => {
  for (const template of ['rest-api-postgres','secret-reload-node']) await t.test(template, async t => {
    const directory=await mkdtemp(join(tmpdir(),'gregale-postgres-runtime-'));
    t.after(()=>rm(directory,{recursive:true,force:true}));
    const source=new URL(`../../../cmd/gregale/templates/${template}/`,import.meta.url);
    await cp(new URL('runtime/',source),join(directory,'runtime'),{recursive:true});
    await cp(new URL('database.js',source),join(directory,'database.js'));
    await writeFile(join(directory,'package.json'),JSON.stringify({type:'module'}));
    const {createDatabase}=await import(pathToFileURL(join(directory,'database.js')).href);
    let retired=0;
    const database=createDatabase(()=>({on(){},connect:async()=>({on(){},off(){},query:async()=>({rows:[]}),release(){}}),
      end:async()=>{retired++;}}),{env:{DATABASE_URL:'postgres://worker:fixture@db:5432/app?sslmode=require'}});
    await database.start();await database.query('SELECT 1');await database.close();
    assert.equal(retired,1);
  });
});

test('isolated runtime subpaths run without template sources and handle real SIGHUP in a child workload', {timeout:30_000}, async t => {
  const directory=await mkdtemp(join(tmpdir(),'gregale-secret-package-'));
  t.after(()=>rm(directory,{recursive:true,force:true}));
  const installed=join(directory,'node_modules','@gregale','sdk-node');
  await mkdir(join(installed,'dist'),{recursive:true});
  // These entry points promise no generated API dependencies. Copy their actual
  // shipped files only; an accidental checkout/API import must fail in the child.
  for(const module of ['secret-reload-runtime','object-storage-runtime','binding-client-runtime','postgres-pool-runtime']) {
    for(const extension of ['js','js.map','d.ts','d.ts.map']) {
      const file=`${module}.${extension}`;
      await cp(new URL(`../dist/${file}`,import.meta.url),join(installed,'dist',file));
    }
  }
  for(const file of ['package.json','README.md','LICENSE'])await cp(new URL(`../${file}`,import.meta.url),join(installed,file));
  const snapshot=join(directory,'snapshot'),ready=join(directory,'ready');
  const secrets={ASSETS_ENDPOINT:'https://s3.gregale.test',ASSETS_REGION:'auto',ASSETS_BUCKET:'assets',
    ASSETS_ADDRESSING_STYLE:'path',ASSETS_ACCESS_KEY_ID:'private-access',ASSETS_SECRET_ACCESS_KEY:'private-key',
    DATABASE_URL:'postgres://worker:private-secret@db.example:5432/app'};
  const publish=async key=>{
    const next=join(directory,'next');await writeFile(next,JSON.stringify({revision:key.repeat(64),secrets}),{mode:0o600});await rename(next,snapshot);
  };
  await publish('a');
  const receipts=[],waiters=[];
  const server=createServer((req,res)=>{
    void (async()=>{
      let raw='';for await(const chunk of req)raw+=String(chunk);
      const body=JSON.parse(raw);receipts.push(body);
      assert.deepEqual(Object.keys(body).sort(),['generation','revision','status']);
      assert.equal(body.status,'applied');assert.equal(body.generation,'d'.repeat(32));
      res.writeHead(202).end();waiters.shift()?.();
    })().catch(()=>res.writeHead(500).end());
  });
  server.listen(0,'127.0.0.1');await once(server,'listening');
  t.after(()=>new Promise((resolve,reject)=>server.close(e=>e?reject(e):resolve())));
  const address=server.address();assert.ok(address&&typeof address!=='string');
  const script=`
    import {SecretReloadCoordinator} from '@gregale/sdk-node/secrets/runtime';
    import {ObjectStorageBinding} from '@gregale/sdk-node/object-storage/runtime';
    import {PostgresPoolBinding} from '@gregale/sdk-node/postgres/runtime';
    const binding=new ObjectStorageBinding('ASSETS',()=>({}));
    const database=new PostgresPoolBinding('DATABASE_URL',()=>({}),{validate:()=>{},retire:()=>{}});
    const shutdown=new AbortController();
    const coordinator=new SecretReloadCoordinator({consumers:[binding.secretConsumer(),database.secretConsumer()],signal:shutdown.signal});
    process.on('message',()=>{}); // Stand in for the application's resident request loop.
    process.on('SIGTERM',()=>{shutdown.abort();process.disconnect();});
    await coordinator.start();
    process.send({ready:binding.current().bucket});
  `;
  const child=spawn(process.execPath,['--input-type=module','-e',script],{cwd:directory,
    env:{...process.env,FAAS_SECRETS_SNAPSHOT_FILE:snapshot,FAAS_SECRETS_RELOAD_READY_FILE:ready,
      FAAS_SECRETS_RELOAD_ACK_ENDPOINT:`http://127.0.0.1:${address.port}/ack`,FAAS_SECRETS_RELOAD_GENERATION:'d'.repeat(32)},
    stdio:['ignore','pipe','pipe','ipc']});
  let output='';child.stdout.on('data',chunk=>{output+=String(chunk);});child.stderr.on('data',chunk=>{output+=String(chunk);});
  t.after(()=>{if(child.exitCode===null)child.kill('SIGKILL');});
  let stopping=false;
  const exit=once(child,'exit');
  const prematureExit=exit.then(()=>{
    if(!stopping)throw new Error('Workload exited before qualification');
    return new Promise(()=>{});
  });
  void prematureExit.catch(()=>{});
  const nextAck=()=>new Promise(resolve=>waiters.push(resolve));
  const messages=once(child,'message');
  const first=nextAck();await Promise.race([first,prematureExit]);
  const [message]=await Promise.race([messages,prematureExit]);assert.deepEqual(message,{ready:'assets'});
  assert.equal(await readFile(ready,'utf8'),'ready\n');
  await publish('b');const second=nextAck();assert.equal(child.kill('SIGHUP'),true);await Promise.race([second,prematureExit]);
  assert.deepEqual(receipts.map(r=>r.revision),['a'.repeat(64),'b'.repeat(64)]);
  stopping=true;child.kill('SIGTERM');const [code,signal]=await exit;
  assert.equal(code,0);assert.equal(signal,null);assert.equal(output,'');
});

test('secret runtime remains outside the browser import graph', async () => {
  const visited=new Set();
  const walk=async path=>{
    if(visited.has(path.href))return;visited.add(path.href);
    const source=await readFile(path,'utf8');
    assert.ok(!source.includes('secret-reload-runtime'));assert.ok(!source.includes('node:fs'));assert.ok(!source.includes('node:http'));
    assert.ok(!source.includes('postgres-pool-runtime'));
    for(const match of source.matchAll(/(?:from\s*|import\s*)['"]([^'"]+)['"]/g)){
      if(match[1].startsWith('.'))await walk(new URL(match[1],path));
    }
  };
  await walk(new URL('../dist/browser.js',import.meta.url));
  assert.ok(visited.size>1);
});
