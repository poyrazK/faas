'use strict';
(() => {
  if(document.body.dataset.staff!=='true')return;
  const $=id=>document.getElementById(id);
  const node=(tag,text,cls)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;if(cls)n.className=cls;return n;};
  const localDay=()=>{const d=new Date();return `${d.getFullYear()}-${String(d.getMonth()+1).padStart(2,'0')}-${String(d.getDate()).padStart(2,'0')}`;};
  const localMinute=value=>{const d=new Date(value);return `${d.getFullYear()}-${String(d.getMonth()+1).padStart(2,'0')}-${String(d.getDate()).padStart(2,'0')}T${String(d.getHours()).padStart(2,'0')}:${String(d.getMinutes()).padStart(2,'0')}`;};
  const when=value=>new Date(value).toLocaleString(undefined,{dateStyle:'medium',timeStyle:'short'});
  let team=null,alertCursor='',alertNext='',alertDay='',alertGeneration=0,lead=null,historyNext='',historyGeneration=0,entries=[],saving=false,attempt=null,operationID='',clock=null,poll=null;
  async function request(path,options={}){
    const response=await fetch(`/v1/sales/${path}`,{credentials:'same-origin',cache:'no-store',...options,headers:{'Content-Type':'application/json'}});
    const data=await response.json().catch(()=>({}));if(!response.ok){const e=new Error(data.detail||'Sales history is unavailable. Try again shortly.');e.code=data.code;e.status=response.status;throw e;}return data;
  }
  function error(id,e){$(id).textContent=e.message;$(id).hidden=false;}
  function button(text,fn){const b=node('button',text,'text-button');b.type='button';b.addEventListener('click',fn);return b;}
  async function loadAlerts(){
    const day=localDay();if(day!==alertDay){alertDay=day;alertCursor='';}
    const generation=++alertGeneration;$('attention-error').hidden=true;$('attention-next').disabled=true;
    const q=new URLSearchParams({kind:$('attention-kind').value,owner:$('attention-owner').value,on:day,after:alertCursor});
    try{
      const data=await request(`alerts?${q}`);if(generation!==alertGeneration)return;
      alertNext=data.next_cursor||'';const c=data.summary;
      $('attention-summary').replaceChildren(...[['Leads needing attention',c.leads],['Stalled open leads',c.stale],['Overdue follow-ups',c.overdue_tasks]].map(([title,count])=>{const card=node('article',undefined,'metric');card.append(node('span',title,'eyebrow'),node('strong',String(count)));return card;}));
      $('attention-definition').textContent=`Open leads are stalled after ${data.stale_days} days without a recorded call, email or demo. Notes and lead edits do not reset this clock. Overdue follow-ups include every lead stage; dates use your local calendar.`;
      const rows=data.leads.map(item=>{
        const row=node('tr'),name=node('td');name.append(node('strong',item.name),node('small',item.email));
        const reasons=node('td');if(item.stale)reasons.append(node('span','Stalled','status task-overdue'));if(item.overdue_tasks)reasons.append(node('small',`${item.overdue_tasks} overdue follow-up${item.overdue_tasks===1?'':'s'} · earliest ${item.earliest_due_on}`));
        const contact=node('td');contact.append(node('span',when(item.last_contact_at)),node('small',item.has_contact?'Last recorded contact':'Lead created · no contact recorded'));
        const action=node('td');action.append(button('View activity',()=>openHistory(item.id)));row.append(name,node('td',item.owner_email),reasons,contact,action);return row;
      });
      if(!rows.length){const row=node('tr'),cell=node('td','No leads need attention in this view.','empty');cell.colSpan=5;row.append(cell);rows.push(row);}
      $('attention-rows').replaceChildren(...rows);$('attention-status').textContent=`${data.leads.length} leads on this page · checked ${when(data.observed_at)}`;
      $('attention-next').disabled=!alertNext;$('attention-first').hidden=!alertCursor;
    }catch(e){if(generation!==alertGeneration)return;alertNext='';$('attention-summary').replaceChildren();$('attention-rows').replaceChildren();$('attention-status').textContent='Lead alerts are unavailable.';error('attention-error',e);}
  }
  function renderHistory(){
    $('activity-history').replaceChildren(...entries.map(item=>{
      const article=node('article',undefined,'activity-entry');const heading=node('div',undefined,'activity-entry-heading');
      heading.append(node('span',item.kind.toUpperCase(),'eyebrow'),node('time',when(item.occurred_at),'muted'));article.append(heading,node('p',item.summary,'activity-text'));
      if(item.outcome)article.append(node('strong','Outcome'),node('p',item.outcome,'activity-text'));
      if(item.next_step)article.append(node('strong','Next step'),node('p',item.next_step,'activity-text'));
      if(item.follow_up_title)article.append(node('p',`Follow-up: ${item.follow_up_title} · due ${item.follow_up_on}${item.task_id?'':' · task no longer exists'}`,'muted'));
      article.append(node('small',`Recorded by ${item.actor_email||'a former team member'} · ${when(item.recorded_at)}`,'muted'));return article;
    }));
    if(!entries.length)$('activity-history').append(node('p','No activities recorded yet. Add the first conversation or note.','muted'));
    $('activity-older').hidden=!historyNext;
  }
  function lockForm(){
    for(const field of $('activity-form').elements)field.disabled=saving||Boolean(attempt)||!lead?.version;
    $('activity-save').disabled=saving||!lead?.version;
    $('activity-save').textContent=attempt?'Retry save':'Record activity';
    $('activity-new').disabled=saving||!lead?.version;
    $('activity-refresh').disabled=saving;
  }
  async function loadHistory(more=false){
    if(!lead)return;const id=lead.id,generation=++historyGeneration;$('activity-load-error').hidden=true;$('activity-older').disabled=true;
    try{
      const data=await request(`leads/${id}/activities?${new URLSearchParams({after:more?historyNext:''})}`);if(generation!==historyGeneration)return false;
      lead=data.lead;clock=data.observed_at;if(!$('activity-when').value)$('activity-when').value=localMinute(clock);$('activity-lead').textContent=`${lead.name} · ${lead.email}`;$('activity-owner').textContent=`${lead.owner_email} · ${lead.stage}`;
      entries=more?[...entries,...data.activities]:data.activities;historyNext=data.next_cursor||'';renderHistory();lockForm();return true;
    }catch(e){if(generation!==historyGeneration)return false;error('activity-load-error',e);if(e.status===403||e.status===404){entries=[];lead=null;$('activity-form').reset();renderHistory();$('activity-lead').textContent='Lead unavailable';$('activity-owner').textContent='';lockForm();}return false;}
    finally{if(generation===historyGeneration)$('activity-older').disabled=false;}
  }
  function newEntry(){
    if(saving)return;attempt=null;operationID=crypto.randomUUID();$('activity-form').reset();$('activity-when').value=localMinute(clock||new Date());$('activity-follow-up-on').value=localDay();$('activity-follow-up-fields').hidden=true;for(const id of ['activity-follow-up-title','activity-follow-up-on'])$(id).required=false;$('activity-form-error').textContent='';lockForm();
  }
  async function openHistory(id){
    if(saving||!id)return;$('activity-form').reset();for(const field of ['activity-follow-up-title','activity-follow-up-on'])$(field).required=false;$('activity-follow-up-fields').hidden=true;lead={id};entries=[];historyNext='';attempt=null;operationID=crypto.randomUUID();$('activity-lead').textContent='Loading lead…';$('activity-owner').textContent='';$('activity-history').replaceChildren();$('activity-form-error').textContent='';$('activity-older').hidden=true;$('activity-dialog').showModal();
    lockForm();if(await loadHistory())newEntry();
  }
  $('activity-form').addEventListener('submit',async event=>{
    event.preventDefault();if(saving||!lead)return;
    if(!attempt){const occurred=new Date($('activity-when').value);if(!Number.isFinite(occurred.getTime())){$('activity-form-error').textContent='Choose a valid occurrence time.';return;}
      attempt={id:operationID,lead_version:lead.version,kind:$('activity-kind').value,summary:$('activity-summary').value,outcome:$('activity-outcome').value,next_step:$('activity-next').value,occurred_at:occurred.toISOString(),follow_up_title:$('activity-follow-up').checked?$('activity-follow-up-title').value:'',follow_up_on:$('activity-follow-up').checked?$('activity-follow-up-on').value:''};
    }
    saving=true;lockForm();$('activity-form-error').textContent='';const target=lead.id;
    try{await request(`leads/${target}/activities`,{method:'POST',body:JSON.stringify(attempt)});attempt=null;saving=false;await loadHistory();newEntry();document.dispatchEvent(new Event('sales-activity-updated'));await loadAlerts();}
    catch(e){saving=false;if(e.status>=400&&e.status<500){attempt=null;if(e.status===409)await loadHistory();$('activity-form-error').textContent=e.message;}else{$('activity-form-error').textContent='The save could not be confirmed. Retry this entry before starting another; its details are kept unchanged.';}}
    finally{saving=false;lockForm();}
  });
  $('activity-follow-up').addEventListener('change',()=>{const checked=$('activity-follow-up').checked;$('activity-follow-up-fields').hidden=!checked;for(const id of ['activity-follow-up-title','activity-follow-up-on'])$(id).required=checked;});
  $('activity-new').addEventListener('click',()=>{if(attempt&&!confirm('Start a new entry? First check the history for the earlier save.'))return;newEntry();});
  $('activity-close').addEventListener('click',()=>{if(!saving){historyGeneration++;$('activity-dialog').close();}});
  $('activity-dialog').addEventListener('cancel',event=>{if(saving)event.preventDefault();else historyGeneration++;});
  $('activity-refresh').addEventListener('click',()=>loadHistory());$('activity-older').addEventListener('click',()=>loadHistory(true));
  document.addEventListener('sales-open-activity',event=>openHistory(event.detail?.leadId));
  for(const id of ['attention-kind','attention-owner'])$(id).addEventListener('change',()=>{alertCursor='';loadAlerts();});
  $('attention-first').addEventListener('click',()=>{alertCursor='';loadAlerts();});$('attention-next').addEventListener('click',()=>{alertCursor=alertNext;loadAlerts();});$('attention-refresh').addEventListener('click',loadAlerts);$('refresh').addEventListener('click',()=>team?loadAlerts():bootstrap());
  document.addEventListener('sales-pipeline-updated',()=>loadAlerts());document.addEventListener('sales-inquiry-updated',()=>loadAlerts());
  async function bootstrap(){try{team=await request('team');$('attention-owner').replaceChildren(new Option('All salespeople',''),...team.members.map(rep=>new Option(rep.email,rep.id)));$('attention-owner-label').hidden=!team.manager;await loadAlerts();clearInterval(poll);poll=setInterval(()=>{if(!document.hidden&&!saving&&!$('activity-dialog').open)loadAlerts();},team.refresh_seconds*1000);}catch(e){error('attention-error',e);}}
  bootstrap();
})();
