import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { FaaSClient, ManagedPostgresService, type ManagedPostgresResize } from '../src/index.js';

test('compute resize preserves the durable UUID across requests and reads progress', async (t) => {
 const id='33333333-3333-4333-8333-333333333333';
 const progress: ManagedPostgresResize={id,database_id:'orders',from_class:'development',target_class:'burstable',generation:2,state:'pending',connection_interruption_expected:true,created_at:'2026-10-05T00:00:00Z'};
 let posts=0,gets=0;
 const server=createServer(async(req,res)=>{
 assert.equal(req.headers.authorization,'Bearer fixture');
 if(req.method==='POST'){
 assert.equal(req.url,'/v1/postgres/databases/orders/resize');
 let body='';for await (const part of req) body+=part;
 assert.deepEqual(JSON.parse(body),{request_id:id,service_class:'burstable'});posts++;res.statusCode=202;
 }else {assert.equal(req.method,'GET');assert.equal(req.url,`/v1/postgres/databases/orders/resizes/${id}`);gets++;}
 res.setHeader('Content-Type','application/json');res.end(JSON.stringify(progress));
 });server.listen(0,'127.0.0.1');await once(server,'listening');
 t.after(()=>new Promise<void>((resolve,reject)=>server.close(e=>e?reject(e):resolve())));
 const addr=server.address();assert.ok(addr && typeof addr!=='string');
 new FaaSClient(`http://127.0.0.1:${addr.port}`,{token:'fixture',retry:{maxAttempts:1,backoffMs:0}});
 for(let i=0;i<2;i++) assert.deepEqual(await ManagedPostgresService.resizeManagedPostgresDatabase({id:'orders',requestBody:{request_id:id,service_class:'burstable'}}),progress);
 assert.deepEqual(await ManagedPostgresService.getManagedPostgresResize({id:'orders',resizeId:id}),progress);
 assert.equal(posts,2);assert.equal(gets,1);
});
