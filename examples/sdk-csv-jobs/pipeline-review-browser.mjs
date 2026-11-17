import {createLiveJob} from '/sdk/live-jobs.js';
import {realtimeLiveJobSource} from '/sdk/live-job-realtime.js';
import {createBrowserRealtimeTicketSocketFactory} from '/sdk/browser-realtime.js';
import {RealtimeConfigurationError} from '/sdk/realtime-resume.js';
const el=id=>document.getElementById(id);
el('run').value=new URL(location.href).searchParams.get('run')??'';
let controller,live,gate,session=0;
function clear(){session++;controller?.abort();live?.close();gate=null;el('review').hidden=true;el('data').textContent=el('binding').textContent=el('expiry').textContent='';el('approve').disabled=el('reject').disabled=el('report').disabled=true;}
function render(value){gate=value;el('review').hidden=!gate;if(!gate)return;el('data').textContent=JSON.stringify(gate.review,null,2);el('binding').textContent=gate.binding_sha256;el('expiry').textContent=`Expires ${gate.expires_at}${gate.decision?' · '+gate.decision.state:''}`;el('approve').disabled=el('reject').disabled=!gate.can_decide;}
async function attach(){
  clear();const active=new AbortController();controller=active;const current=session,id=el('run').value,token=el('token').value;
  const request=async(action,init={})=>{const res=await fetch(`/runs/${id}/${action}`,{...init,signal:active.signal,headers:{'Content-Type':'application/json',Authorization:'Bearer '+token}});if(!res.ok){const e=new Error((await res.json()).code);e.status=res.status;throw e;}const value=await res.json();active.signal.throwIfAborted();if(current!==session)throw new Error('Access changed');return value;};
  const ticket=async()=>{try{return await request('ticket');}catch(e){if([401,403,404].includes(e.status))throw new RealtimeConfigurationError('Access changed');throw e;}};
  const first=await ticket();let initial=first;
  live=createLiveJob({resourceId:id,timeoutMs:120000,signal:active.signal,loadSnapshot:()=>request('snapshot'),source:realtimeLiveJobSource({url:first.url,channel:first.channel,webSocketFactory:createBrowserRealtimeTicketSocketFactory(async()=>{const value=initial??await ticket();initial=undefined;if(value.url!==first.url||value.channel!==first.channel)throw new RealtimeConfigurationError('Scope changed');return value.ticket;})})});
  el('approve').onclick=()=>decision('approved');el('reject').onclick=()=>decision('rejected');
  async function decision(state){
    if(!gate||gate.decision)return;el('approve').disabled=el('reject').disabled=true;
    try{const result=await request('decision',{method:'POST',body:JSON.stringify({gate_id:gate.id,binding_sha256:gate.binding_sha256,state,payload:{reason:el('reason').value}})});if(current===session){render(result.gate);el('status').textContent='Decision saved: '+result.gate.decision.state;}}
    catch(e){if(current===session){if([401,403,404].includes(e.status)){clear();el('status').textContent='Access changed. Attach again.';return;}try{render((await request('gate')).gate);el('status').textContent=e.message;}catch{clear();el('status').textContent='Access changed. Attach again.';}}}
  }
  el('report').onclick=()=>{(async()=>{const res=await fetch(`/runs/${id}/report`,{signal:active.signal,headers:{Authorization:'Bearer '+token}});if(!res.ok)throw new Error('Report unavailable');const blob=await res.blob();if(current!==session)return;const url=URL.createObjectURL(blob),a=document.createElement('a');a.href=url;a.download='rejections.csv';a.click();setTimeout(()=>URL.revokeObjectURL(url),1000);})().catch(()=>{if(current===session){clear();el('status').textContent='Report unavailable. Attach again.';}});};
  for await(const snapshot of live.updates()){
    if(current!==session)return;
    render((await request('gate')).gate);
    el('status').textContent=snapshot.waiting?'Awaiting approval':`${snapshot.status}: ${snapshot.succeeded}/${snapshot.total} tasks completed`;
    el('report').disabled=snapshot.report!=='ready';
  }
}
document.querySelector('form').onsubmit=e=>{e.preventDefault();const attempt=session+1;attach().catch(e=>{if(session===attempt&&e.name!=='AbortError'){clear();el('status').textContent=e.message;}});};
for(const id of ['token','run'])el(id).addEventListener('input',()=>{clear();el('status').textContent='Access changed. Attach again.';});
