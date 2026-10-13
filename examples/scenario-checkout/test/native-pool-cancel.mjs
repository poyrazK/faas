import assert from "node:assert/strict";
import { readFile, writeFile, rename } from "node:fs/promises";
import { isAbsolute } from "node:path";
import { pathToFileURL } from "node:url";

export async function verifyPoolCancellation(stage, {appURL, namespace, marker, proofPath, emit=console.log}) {
  assert.match(namespace,/^[0-9a-f]{32}$/);assert.ok(isAbsolute(proofPath));
  const status=async()=>{
    const response=await fetch(appURL+"/pool-status",{redirect:"error",signal:AbortSignal.timeout(1000)});
    assert.equal(response.status,200);const text=await response.text();assert.ok(text.length<=4096);
    const value=JSON.parse(text);assert.equal(value.namespace,namespace);
    assert.equal(value.queue_deadline_ms,10000);assert.equal(value.attempt_deadline_ms,30000);return value;
  };
  const save=async proof=>{await writeFile(proofPath+".tmp",JSON.stringify(proof)+"\n",{mode:0o600});await rename(proofPath+".tmp",proofPath);};
  if(stage==="active"){
    assert.match(marker,/^[0-9a-f]{32}$/);
    const baseline=await status();assert.equal(baseline.active,0);assert.equal(baseline.queued,0);assert.equal(baseline.waiters_created,baseline.waiters_finished);
    const controllers=Array.from({length:4},()=>new AbortController());
    const pending=controllers.map(controller=>fetch(appURL+"/dependency",{signal:controller.signal,redirect:"error"}).then(async response=>{await response.text();return response.status;},()=>0));
    try{
      const until=Date.now()+4000;let observed;
      while(Date.now()<until){const value=await status();assert.equal(value.instance_id,baseline.instance_id);
        if(value.active===2&&value.queued===2&&value.active_connections===2&&value.queue_timers===2&&value.deadline_timers===2){observed=value;break;}
        await new Promise(resolve=>setTimeout(resolve,10));
      }
      assert.ok(observed,"active_cancellation_checkpoint_missing");
      await save({namespace,marker,app_url:appURL,baseline,observed});emit(marker);
      // The harness SIGINTs Gregale. CommandContext kills this helper, closing
      // its four HTTP connections while the fault and long deadlines are active.
      await Promise.all(pending);throw new Error("cancellation_not_issued_while_active");
    }finally{for(const controller of controllers)controller.abort();await Promise.all(pending);}
  }else if(stage==="cleanup"){
    const text=await readFile(proofPath,"utf8");assert.ok(text.length<=65536);const proof=JSON.parse(text);assert.equal(proof.namespace,namespace);assert.ok(proof.checkpoint);
    const started=Date.now();let clean;
    while(Date.now()-started<5000){const value=await status();assert.equal(value.instance_id,proof.baseline.instance_id);
      if(["active","queued","active_connections","queue_timers","deadline_timers"].every(key=>value[key]===0)&&value.waiters_created===value.waiters_finished){clean=value;break;}
      await new Promise(resolve=>setTimeout(resolve,20));
    }
    assert.ok(clean,"cancelled_pool_did_not_join");assert.ok(clean.queued_request_cancellations-proof.baseline.queued_request_cancellations>=2);assert.ok(clean.cancelled_requests-proof.baseline.cancelled_requests>=4);
    assert.equal(clean.waiters_timed_out,proof.baseline.waiters_timed_out);assert.equal(clean.application_timeouts,proof.baseline.application_timeouts);
    proof.cleanup=clean;proof.cleanup_elapsed_ms=Date.now()-started;await save(proof);return proof;
  }else throw new Error("invalid_cancellation_stage");
}
if(process.argv[1]&&import.meta.url===pathToFileURL(process.argv[1]).href){
  try{await verifyPoolCancellation(process.argv[2],{appURL:process.env.GREGALE_TEST_URL,namespace:process.env.GREGALE_TEST_RUN_ID,marker:process.env.GREGALE_NATIVE_CANCEL_MARKER,proofPath:process.env.GREGALE_NATIVE_POOL_CANCEL_PROOF});}
  catch{console.error("Native active pool cancellation failed");process.exitCode=1;}
}
