import assert from "node:assert/strict";
import net from "node:net";
import test from "node:test";
import { createEchoDependency, createRecoveryProbe } from "../native-probe/server.js";

async function listen(server) {
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  return server.address().port;
}
async function fixture(t, delayMS, drop = false) {
  const sockets = new Set();
  const dependency = drop ? net.createServer(socket => socket.on("data", () => {})) : createEchoDependency();
  dependency.on("connection", socket => { sockets.add(socket); socket.on("close", () => sockets.delete(socket)); });
  const port = await listen(dependency);
  const app = createRecoveryProbe({ dependencyHost: "127.0.0.1", dependencyPort: port, delayMS });
  const appPort = await listen(app);
  t.after(async () => {
    for (const socket of sockets) socket.destroy();
    app.closeAllConnections();
    await Promise.all([new Promise(resolve => app.close(resolve)), new Promise(resolve => dependency.close(resolve))]);
  });
  return `http://127.0.0.1:${appPort}`;
}

test("native probe control returns dependency and completed responses", async t => {
  const base = await fixture(t, 25);
  const dependency = await fetch(`${base}/dependency`, { signal: AbortSignal.timeout(2000) });
  assert.equal(dependency.status, 200);
  assert.deepEqual(await dependency.json(), { dependency: "reachable" });
  assert.deepEqual(await (await fetch(`${base}/stalled`)).json(), { completed: true });
});

test("native probe keeps healthy HTTP traffic while a second request stalls", async t => {
  const base = await fixture(t, 15000);
  const controller = new AbortController();
  const stalled = fetch(`${base}/stalled`, { signal: controller.signal });
  // Observe the rejection immediately so cancellation cannot leak a promise.
  const cancelled = assert.rejects(stalled, error => error.name === "AbortError");
  for (let i = 0; i < 3; i++) {
    const response = await fetch(`${base}/dependency`, { signal: AbortSignal.timeout(2000) });
    assert.equal(response.status, 200);
    assert.deepEqual(await response.json(), { dependency: "reachable" });
  }
  controller.abort();
  await cancelled;
});

test("native probe bounds a lost TCP acknowledgement while returning HTTP 200", async t => {
  const base = await fixture(t, 25, true);
  const response = await fetch(`${base}/dependency`, { signal: AbortSignal.timeout(2000) });
  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), { dependency: "unavailable" });
});

test("campaign probe exercises a full bounded payload and accepts fragmented echoes", async t => {
  const sockets=new Set();
  const dependency=net.createServer(socket=>{
    sockets.add(socket);socket.once("close",()=>sockets.delete(socket));
    socket.on("data",chunk=>{socket.write(chunk.subarray(0,3));setTimeout(()=>{if(!socket.destroyed)socket.write(chunk.subarray(3));},10);});
  });
  const port=await listen(dependency),app=createRecoveryProbe({dependencyHost:"127.0.0.1",dependencyPort:port,delayMS:25,payloadBytes:16384});
  const appPort=await listen(app);
  t.after(async()=>{for(const socket of sockets)socket.destroy();app.closeAllConnections();await Promise.all([new Promise(resolve=>app.close(resolve)),new Promise(resolve=>dependency.close(resolve))]);});
  assert.deepEqual(await(await fetch(`http://127.0.0.1:${appPort}/dependency`)).json(),{dependency:"reachable"});
});

test("campaign echo bounds its enlarged payload and rejects unsupported limits", async t => {
  assert.throws(()=>createEchoDependency({maxBytes:65536}),/invalid echo limit/);
  assert.throws(()=>createRecoveryProbe({delayMS:25,payloadBytes:65536}),/invalid probe payload/);
  const dependency=createEchoDependency({maxBytes:16384}),port=await listen(dependency);
  t.after(()=>new Promise(resolve=>dependency.close(resolve)));
  const socket=net.connect({host:"127.0.0.1",port});
  const chunks=[];
  await new Promise((resolve,reject)=>{socket.on("error",reject);socket.on("data",chunk=>{chunks.push(chunk);if(Buffer.concat(chunks).length===16384){socket.end();resolve();}});socket.on("connect",()=>socket.write(Buffer.alloc(16384,120)));});
  assert.equal(Buffer.concat(chunks).length,16384);
  await new Promise(resolve=>socket.once("close",resolve));
});

test("connection observation preserves identity and joins TCP sockets after request cancellation", async t => {
  const sockets=new Set();
  const dependency=net.createServer(socket=>{sockets.add(socket);socket.once("close",()=>sockets.delete(socket));socket.on("data",()=>{});});
  const port=await listen(dependency),app=createRecoveryProbe({dependencyHost:"127.0.0.1",dependencyPort:port,delayMS:25,payloadBytes:16384}),appPort=await listen(app),base=`http://127.0.0.1:${appPort}`;
  t.after(async()=>{for(const socket of sockets)socket.destroy();app.closeAllConnections();await Promise.all([new Promise(resolve=>app.close(resolve)),new Promise(resolve=>dependency.close(resolve))]);});
  const baseline=await(await fetch(base+"/connections")).json();assert.equal(baseline.active_connections,0);assert.match(baseline.instance_id,/^[0-9a-f-]{36}$/);
  const controller=new AbortController(),pending=fetch(base+"/dependency",{signal:controller.signal});
  const cancelled=assert.rejects(pending,error=>error.name==="AbortError");
  let observed;
  for(let i=0;i<20;i++){observed=await(await fetch(base+"/connections")).json();if(observed.active_connections===1)break;await new Promise(resolve=>setTimeout(resolve,5));}
  assert.equal(observed.active_connections,1);assert.equal(observed.instance_id,baseline.instance_id);
  controller.abort();await cancelled;
  for(let i=0;i<20;i++){observed=await(await fetch(base+"/connections")).json();if(observed.active_connections===0)break;await new Promise(resolve=>setTimeout(resolve,5));}
  assert.equal(observed.active_connections,0);assert.equal(observed.instance_id,baseline.instance_id);
});
