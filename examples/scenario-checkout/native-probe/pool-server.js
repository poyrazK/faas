import http from "node:http";
import net from "node:net";
import { randomUUID } from "node:crypto";

// Fixed acceptance fixture: two connection permits, two queued callers, no retries.
export function createBoundedConnectionPool({ dependencyHost, dependencyPort, overallDeadlineMS = 0, cancellationFixture = false, namespace = undefined }) {
  if (![0,500].includes(overallDeadlineMS)) throw new Error("invalid_application_deadline");
  if(cancellationFixture && (!/^[0-9a-f]{32}$/.test(namespace||" ") || overallDeadlineMS!==0)) throw new Error("invalid_cancellation_fixture");
  const queueDeadlineMS=cancellationFixture?10000:200,attemptDeadlineMS=cancellationFixture?30000:750;
  const controllers=new Set();
  let applicationTimeouts=0, cancelledRequests=0, queuedRequestCancellations=0, overallTimers=0, attemptTimers=0;
  const instanceID=randomUUID(), sockets=new Set(), queue=[], work=new Set();
  let active=0, peakActive=0, peakQueued=0, created=0, finished=0, cancelled=0, timedOut=0, maxWait=0, completed=0, timers=0, stopped=false, stopping;
  const status=()=>({namespace,queue_deadline_ms:queueDeadlineMS,attempt_deadline_ms:attemptDeadlineMS,instance_id:instanceID,capacity:2,active,queued:queue.length,peak_active:peakActive,peak_queued:peakQueued,
    waiters_created:created,waiters_finished:finished,waiters_cancelled:cancelled,waiters_timed_out:timedOut,max_wait_ms:maxWait,completed,
    active_connections:sockets.size,queue_timers:timers,deadline_timers:overallTimers+attemptTimers,application_timeouts:applicationTimeouts,cancelled_requests:cancelledRequests,queued_request_cancellations:queuedRequestCancellations,stopped});
  const acquired=()=>{active++;peakActive=Math.max(peakActive,active);};
  const acquire=(response,signal,onQueued)=>new Promise(resolve=>{
    if(stopped||response.destroyed||signal.aborted){resolve(false);return;}
    if(active<2){acquired();resolve(true);return;}
    if(queue.length>=2){resolve(false);return;}
    onQueued();created++;const at=performance.now();
    const entry={finish:outcome=>{
      const index=queue.indexOf(entry);if(index<0)return;
      queue.splice(index,1);clearTimeout(entry.timer);timers--;response.off("close",entry.closed);signal.removeEventListener("abort",entry.closed);
      finished++;maxWait=Math.max(maxWait,Math.ceil(performance.now()-at));
      if(outcome==="cancelled")cancelled++;if(outcome==="timeout")timedOut++;
      if(outcome==="acquired")acquired();resolve(outcome==="acquired");
    }};
    entry.closed=()=>entry.finish("cancelled");
    queue.push(entry);peakQueued=Math.max(peakQueued,queue.length);
    timers++;entry.timer=setTimeout(()=>entry.finish("timeout"),queueDeadlineMS);response.once("close",entry.closed);signal.addEventListener("abort",entry.closed,{once:true});
  });
  const release=()=>{active--;if(!stopped&&queue.length)queue[0].finish("acquired");};
  const query=(response,signal)=>new Promise(resolve=>{
    if(stopped||response.destroyed||signal.aborted){resolve(false);return;}
    const socket=net.connect({host:dependencyHost,port:dependencyPort});sockets.add(socket);
    const payload=Buffer.alloc(16384,120);let received=0,done=false,reachable=false;const chunks=[];
    const finish=value=>{if(done)return;done=true;reachable=value;clearTimeout(timer);response.off("close",closed);signal.removeEventListener("abort",closed);attemptTimers--;socket.destroy();};
    attemptTimers++;
    const closed=()=>finish(false),timer=setTimeout(()=>finish(false),attemptDeadlineMS);
    response.once("close",closed);signal.addEventListener("abort",closed,{once:true});
    socket.once("close",()=>{sockets.delete(socket);if(!done)finish(false);resolve(reachable);});
    socket.once("connect",()=>socket.write(payload));
    socket.on("data",chunk=>{received+=chunk.length;if(received>payload.length){finish(false);return;}chunks.push(chunk);if(received===payload.length)finish(Buffer.concat(chunks).equals(payload));});
    socket.once("error",()=>finish(false));socket.once("end",()=>finish(false));
  });
  const handle=async(request,response)=>{
    let code=200,body;
    if(request.method!=="GET"){code=405;body={error:"method_not_allowed"};}
    else if(request.url==="/pool-status")body=status();
    else if(request.url==="/connections")body={instance_id:instanceID,active_connections:sockets.size};
    else if(request.url==="/")body={ready:!stopped};
    else if(request.url==="/dependency"){
      const controller=new AbortController();controllers.add(controller);
      let expired=false,wasQueued=false;
      const disconnected=()=>{if(!controller.signal.aborted){cancelledRequests++;if(wasQueued)queuedRequestCancellations++;controller.abort();}};
      response.once("close",disconnected);
      const timer=overallDeadlineMS?setTimeout(()=>{expired=true;applicationTimeouts++;controller.abort();},overallDeadlineMS):undefined;
      if(timer)overallTimers++;
      try {
        if(!await acquire(response,controller.signal,()=>{wasQueued=true;})){code=503;body={error:"pool_unavailable"};}
        else {try{const reachable=await query(response,controller.signal);if(reachable){completed++;body={dependency:"reachable"};}else{code=503;body={error:"dependency_unavailable"};}}finally{release();}}
        // Return an application timeout only after this request's socket or waiter
        // has joined. The overall timer spans both permit acquisition and I/O.
        if(expired){code=504;body={error:"deadline_exceeded",retryable:true};}
      } finally {
        if(timer){clearTimeout(timer);overallTimers--;}
        response.off("close",disconnected);controllers.delete(controller);
      }
    }else{code=404;body={error:"not_found"};}
    if(!response.destroyed){response.writeHead(code,{"content-type":"application/json","cache-control":"no-store"});response.end(JSON.stringify(body));}
  };
  const stop=()=>stopping||=(async()=>{stopped=true;for(const controller of controllers)controller.abort();for(const entry of [...queue])entry.finish("cancelled");for(const socket of sockets)socket.destroy();await Promise.all([...work]);})();
  const server=http.createServer((request,response)=>{
    const pending=handle(request,response);work.add(pending);
    pending.finally(()=>work.delete(pending)).catch(()=>{if(!response.destroyed){response.writeHead(503);response.end();}});
  });
  server.once("close",()=>{stop().catch(()=>{});});
  return {server,status,stop};
}
