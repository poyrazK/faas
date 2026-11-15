import {createLiveJob} from '/sdk/live-jobs.js';
import {realtimeLiveJobSource} from '/sdk/live-job-realtime.js';
import {createBrowserRealtimeTicketSocketFactory} from '/sdk/browser-realtime.js';
import {RealtimeConfigurationError} from '/sdk/realtime-resume.js';
const el=id=>document.getElementById(id);el('run').value=new URL(location.href).searchParams.get('run')??'';
let controller,live,session=0;
function clear(){session++;controller?.abort();live?.close();el('children').replaceChildren();el('totals').textContent='';}
async function attach(){
  clear();const current=session,active=new AbortController();controller=active;const id=el('run').value,token=el('token').value;
  const request=async action=>{const r=await fetch(`/batches/${id}/${action}`,{signal:active.signal,headers:{Authorization:'Bearer '+token}});if(!r.ok)throw new RealtimeConfigurationError('Access unavailable');const value=await r.json();active.signal.throwIfAborted();if(current!==session)throw new Error('Access changed');return value;};
  const first=await request('ticket');let initial=first;
  live=createLiveJob({resourceId:id,timeoutMs:240000,signal:active.signal,loadSnapshot:()=>request('snapshot'),source:realtimeLiveJobSource({url:first.url,channel:first.channel,webSocketFactory:createBrowserRealtimeTicketSocketFactory(async()=>{const next=initial??await request('ticket');initial=undefined;if(next.channel!==first.channel||next.url!==first.url)throw new RealtimeConfigurationError('Scope changed');return next.ticket;})})});
  for await(const snapshot of live.updates()){
    const {view,output}=await request('view');if(current!==session)return;
    el('status').textContent=snapshot.waiting?'Awaiting child approval':`${snapshot.status}: ${snapshot.succeeded+snapshot.failed+snapshot.cancelled}/${snapshot.total} runs finished, ${snapshot.failed} failed`;
    el('totals').textContent=output?`${output.saved} rows saved; ${output.rejected} rows rejected`:'';
    el('children').replaceChildren(...view.children.map(c=>{const li=document.createElement('li');li.textContent=`${c.id}: ${c.output?.status??c.phase}${c.waiting?' · awaiting '+c.waiting.kind:''}`;if(c.run_id){const a=document.createElement('a');a.href='/?run='+encodeURIComponent(c.run_id);a.textContent='Open import';li.append(a);}return li;}));
  }
}
document.querySelector('form').onsubmit=e=>{e.preventDefault();const attempt=session+1;attach().catch(()=>{if(session===attempt){clear();el('status').textContent='Access unavailable. Attach again.';}});};
for(const id of ['token','run'])el(id).addEventListener('input',()=>{clear();el('status').textContent='Access changed. Attach again.';});
