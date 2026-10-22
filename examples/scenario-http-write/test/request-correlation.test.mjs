import assert from "node:assert/strict";
import http from "node:http";
import test from "node:test";
import { createNativeRetryCaller } from "../native-retry-app/server.js";

async function listen(server) { await new Promise(resolve => server.listen(0,"127.0.0.1",resolve)); return `http://127.0.0.1:${server.address().port}`; }
async function close(server) { server.closeAllConnections(); await new Promise(resolve => server.close(resolve)); }

test("retry fixture propagates one scoped ID through retries and acknowledges completed attempts", { timeout: 3000 }, async t => {
  const seen=[], namespace="a".repeat(32), requestID=namespace+"."+"b".repeat(16)+".1";
  const dependency=http.createServer((request,response)=>{seen.push(request.headers["x-gregale-test-request-id"]);response.writeHead(503,{"content-type":"application/json"});response.end("{}");});
  const dependencyURL=await listen(dependency), caller=createNativeRetryCaller({dependencyURL,namespace,token:"c".repeat(64)}), appURL=await listen(caller.server);
  t.after(async()=>{await caller.stop();await close(caller.server);await close(dependency);});
  const result=await fetch(appURL+"/query",{headers:{"x-gregale-test-request-id":requestID}});
  assert.equal(result.status,503);assert.equal(result.headers.get("x-gregale-test-dependency-attempts"),"2");assert.equal((await result.json()).attempts,2);assert.deepEqual(seen,[requestID,requestID]);
  const ordinary=await fetch(appURL+"/query");assert.equal(ordinary.headers.get("x-gregale-test-dependency-attempts"),null);await ordinary.json();assert.ok(seen.slice(2).every(value=>value===undefined));
  await caller.stop();assert.equal(caller.status().active_requests,0);assert.equal(caller.status().retry_timers,0);assert.equal(caller.status().deadline_timers,0);
});

for (const qualificationMode of ["outlier", "missing-id", "missing-receipt"]) {
  test(`qualification fixture ${qualificationMode} has bounded, joined failure evidence`, { timeout: 6000 }, async t => {
    const namespace="a".repeat(32), seen=[];
    const dependency=http.createServer((request,response)=>{seen.push(request.headers["x-gregale-test-request-id"]);response.writeHead(503,{"content-type":"application/json"});response.end("{}");});
    const dependencyURL=await listen(dependency), caller=createNativeRetryCaller({dependencyURL,namespace,token:"c".repeat(64),qualificationMode}), appURL=await listen(caller.server);
    t.after(async()=>{await caller.stop();await close(caller.server);await close(dependency);});
    const counts=[];
    for(let slot=1;slot<=10;slot++) {
      const id=namespace+"."+"b".repeat(16)+"."+slot;
      const result=await fetch(appURL+"/query",{headers:{"x-gregale-test-request-id":id}});
      assert.equal(result.status,503);
      const body=await result.json();counts.push(body.attempts);
      assert.equal(result.headers.get("x-gregale-test-dependency-attempts"),qualificationMode==="missing-receipt"?null:String(body.attempts));
    }
    if(qualificationMode==="outlier") {assert.deepEqual(counts,[11,1,1,1,1,1,1,1,1,1]);assert.equal(counts.reduce((a,b)=>a+b)/10,2);}
    else assert.ok(counts.every(value=>value===2));
    assert.equal(seen.length,20);
    assert.ok(seen.every(value=>qualificationMode==="missing-id"?value===undefined:typeof value==="string"));
    await caller.stop();assert.equal(caller.status().active_requests,0);assert.equal(caller.status().retry_timers,0);assert.equal(caller.status().deadline_timers,0);
  });
}
