import assert from "node:assert/strict";
import net from "node:net";
import test from "node:test";
import { createBoundedConnectionPool } from "../native-probe/pool-server.js";

async function listen(server){await new Promise(resolve=>server.listen(0,"127.0.0.1",resolve));return server.address().port;}
async function fixture(t,cancellationFixture=false){
  let stalled=true;const sockets=new Set();
  const dependency=net.createServer(socket=>{sockets.add(socket);socket.once("close",()=>sockets.delete(socket));socket.on("data",chunk=>{if(!stalled)socket.write(chunk);});});
  const port=await listen(dependency),pool=createBoundedConnectionPool({dependencyHost:"127.0.0.1",dependencyPort:port,cancellationFixture,namespace:cancellationFixture?"0123456789abcdef0123456789abcdef":undefined}),appPort=await listen(pool.server);
  t.after(async()=>{await pool.stop();pool.server.closeAllConnections();for(const socket of sockets)socket.destroy();await Promise.all([new Promise(resolve=>pool.server.close(resolve)),new Promise(resolve=>dependency.close(resolve))]);});
  return {pool,base:`http://127.0.0.1:${appPort}`,recover:()=>{stalled=false;}};
}
async function until(predicate){for(let i=0;i<100;i++){if(predicate())return;await new Promise(resolve=>setTimeout(resolve,5));}assert.ok(predicate(),"bounded fixture condition was not observed");}

test("full pool bounds queues and wait times while health remains responsive",{timeout:5000},async t=>{
  const {pool,base,recover}=await fixture(t);
  const requests=Array.from({length:8},()=>fetch(base+"/dependency"));
  await until(()=>pool.status().active===2&&pool.status().queued===2);
  const at=performance.now();assert.deepEqual(await(await fetch(base+"/")).json(),{ready:true});assert.ok(performance.now()-at<750);
  const responses=await Promise.all(requests);assert.ok(responses.every(response=>response.status===503));await Promise.all(responses.map(response=>response.json()));
  let status=pool.status();assert.equal(status.peak_active,2);assert.equal(status.peak_queued,2);assert.equal(status.waiters_created,2);assert.equal(status.waiters_finished,2);assert.equal(status.waiters_timed_out,2);assert.ok(status.max_wait_ms>=200&&status.max_wait_ms<=300);
  assert.equal(status.queued,0);assert.equal(status.active,0);assert.equal(status.queue_timers,0);assert.equal(status.active_connections,0);
  recover();for(let i=0;i<10;i++){const response=await fetch(base+"/dependency");assert.equal(response.status,200);assert.deepEqual(await response.json(),{dependency:"reachable"});}
  status=pool.status();assert.equal(status.completed,10);assert.equal(status.active,0);assert.equal(status.instance_id,(await(await fetch(base+"/pool-status")).json()).instance_id);
});

test("queued cancellation releases timers and active request cancellation releases permits",{timeout:5000},async t=>{
  const {pool,base}=await fixture(t),controllers=Array.from({length:4},()=>new AbortController());
  const pending=controllers.map(controller=>fetch(base+"/dependency",{signal:controller.signal}));
  const cancelled=pending.map(promise=>assert.rejects(promise,error=>error.name==="AbortError"));
  await until(()=>pool.status().active===2&&pool.status().queued===2);
  controllers[2].abort();controllers[3].abort();
  await until(()=>pool.status().queued===0);
  controllers[0].abort();controllers[1].abort();await Promise.all(cancelled);
  await until(()=>pool.status().active===0&&pool.status().active_connections===0);
  const status=pool.status();assert.equal(status.waiters_created,2);assert.equal(status.waiters_finished,2);assert.equal(status.waiters_cancelled,2);assert.equal(status.queue_timers,0);
});

test("stopping an exhausted pool joins waiters, sockets and permit owners",{timeout:5000},async t=>{
  const {pool,base}=await fixture(t);
  const pending=Array.from({length:4},()=>fetch(base+"/dependency"));await until(()=>pool.status().active===2&&pool.status().queued===2);
  await pool.stop();await Promise.all(pending.map(async promise=>{const response=await promise;await response.arrayBuffer();}));
  const status=pool.status();assert.equal(status.stopped,true);for(const field of ["active","queued","active_connections","queue_timers"])assert.equal(status[field],0);
  assert.equal(status.waiters_created,status.waiters_finished);assert.equal(status.waiters_cancelled,2);
});

test("application deadline covers the complete request and joins dependency work before 504",{timeout:5000},async t=>{
  const sockets=new Set();
  const dependency=net.createServer(socket=>{sockets.add(socket);socket.once("close",()=>sockets.delete(socket));socket.on("data",()=>{});});
  const port=await listen(dependency),pool=createBoundedConnectionPool({dependencyHost:"127.0.0.1",dependencyPort:port,overallDeadlineMS:500}),appPort=await listen(pool.server),base=`http://127.0.0.1:${appPort}`;
  t.after(async()=>{await pool.stop();pool.server.closeAllConnections();for(const socket of sockets)socket.destroy();await Promise.all([new Promise(resolve=>pool.server.close(resolve)),new Promise(resolve=>dependency.close(resolve))]);});
  const started=performance.now(),response=await fetch(base+"/dependency");assert.equal(response.status,504);assert.deepEqual(await response.json(),{error:"deadline_exceeded",retryable:true});
  assert.ok(performance.now()-started>=490&&performance.now()-started<750);
  const status=pool.status();assert.equal(status.application_timeouts,1);for(const key of ["active","queued","active_connections","deadline_timers"])assert.equal(status[key],0);
  const controller=new AbortController(),pending=fetch(base+"/dependency",{signal:controller.signal}),cancelled=assert.rejects(pending,error=>error.name==="AbortError");
  await until(()=>pool.status().active===1);controller.abort();await cancelled;await until(()=>pool.status().active===0);
  assert.equal(pool.status().cancelled_requests,1);assert.equal(pool.status().application_timeouts,1);assert.equal(pool.status().deadline_timers,0);
});


test("sealed native cancellation mode retires live work through disconnects before deadlines",{timeout:5000},async t=>{
  const {pool,base}=await fixture(t,true),controllers=Array.from({length:4},()=>new AbortController());
  const pending=controllers.map(controller=>fetch(base+"/dependency",{signal:controller.signal}).catch(error=>error.name));
  await until(()=>pool.status().active===2&&pool.status().queued===2);
  const active=pool.status();assert.equal(active.namespace,"0123456789abcdef0123456789abcdef");assert.equal(active.queue_deadline_ms,10000);assert.equal(active.attempt_deadline_ms,30000);assert.equal(active.queue_timers,2);assert.equal(active.deadline_timers,2);
  for(const controller of controllers)controller.abort();await Promise.all(pending);
  await until(()=>pool.status().active===0&&pool.status().queued===0&&pool.status().active_connections===0);
  const clean=pool.status();assert.equal(clean.waiters_created,2);assert.equal(clean.waiters_finished,2);assert.equal(clean.waiters_cancelled,2);assert.equal(clean.cancelled_requests,4);assert.equal(clean.queued_request_cancellations,2);assert.equal(clean.waiters_timed_out,0);assert.equal(clean.queue_timers,0);assert.equal(clean.deadline_timers,0);assert.equal(clean.instance_id,active.instance_id);
});

test("queued caller cancellation remains accounted after a permit is acquired",{timeout:5000},async t=>{
  const {pool,base}=await fixture(t,true),controllers=Array.from({length:4},()=>new AbortController());
  const pending=controllers.map(controller=>fetch(base+"/dependency",{signal:controller.signal}).catch(error=>error.name));
  await until(()=>pool.status().active===2&&pool.status().queued===2);
  controllers[0].abort();controllers[1].abort();
  await until(()=>pool.status().queued===0&&pool.status().waiters_finished===2&&pool.status().active===2);
  controllers[2].abort();controllers[3].abort();await Promise.all(pending);
  await until(()=>pool.status().active===0&&pool.status().active_connections===0);
  const clean=pool.status();assert.equal(clean.waiters_cancelled,0);assert.equal(clean.waiters_created,2);assert.equal(clean.waiters_finished,2);assert.equal(clean.queued_request_cancellations,2);assert.equal(clean.cancelled_requests,4);assert.equal(clean.waiters_timed_out,0);assert.equal(clean.queue_timers,0);assert.equal(clean.deadline_timers,0);
});
