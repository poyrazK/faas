'use strict';
(() => {
  if (document.body.dataset.staff !== 'true') return;
  const $ = id => document.getElementById(id);
  const node = (tag, text, cls) => { const n=document.createElement(tag);if(text!==undefined)n.textContent=text;if(cls)n.className=cls;return n; };
  let team=null, leads=[], tasks=[], leadCursor='', taskCursor='', leadNext='', taskNext='';
  let leadGeneration=0, taskGeneration=0, selectedLead=null, selectedTask=null, taskLead=null, saving=false, poll=null, lastDay='';
  const today = () => { const d=new Date();return `${d.getFullYear()}-${String(d.getMonth()+1).padStart(2,'0')}-${String(d.getDate()).padStart(2,'0')}`; };
  const dayLabel = value => new Date(`${value}T12:00:00`).toLocaleDateString(undefined,{year:'numeric',month:'short',day:'numeric'});
  const label = value => value.charAt(0).toUpperCase()+value.slice(1);
  async function request(path, options={}) {
    const response=await fetch(`/v1/sales/${path}`,{credentials:'same-origin',cache:'no-store',...options,headers:{'Content-Type':'application/json',...options.headers}});
    const data=await response.json().catch(()=>({}));
    if(!response.ok){const error=new Error(data.detail||'Sales data is unavailable. Refresh and try again.');error.code=data.code;throw error;}
    return data;
  }
  function error(id,err){$(id).textContent=err.message;$(id).hidden=false;}
  function empty(id,text,columns){const row=node('tr');const cell=node('td',text,'empty');cell.colSpan=columns;row.append(cell);$(id).replaceChildren(row);}
  function button(text,fn){const b=node('button',text,'text-button');b.type='button';b.addEventListener('click',fn);return b;}
  function options(id,values,selected,blank){const target=$(id);target.replaceChildren();if(blank)target.append(new Option(blank,''));for(const [value,text] of values)target.append(new Option(text,value));target.value=selected||'';}
  function ownerOptions(){return team.members.map(rep=>[rep.id,rep.email]);}
  function renderStages(summary){
    const counts=new Map(summary.map(row=>[row.stage,row.count]));
    $('pipeline-summary').replaceChildren(...team.stages.map(stage=>{
      const b=button(label(stage),()=>{$('prospect-stage').value=stage;leadCursor='';loadLeads();});b.className='stage-card';b.setAttribute('aria-pressed',String($('prospect-stage').value===stage));b.append(node('strong',String(counts.get(stage)||0)));return b;
    }));
  }
  function renderLeads(){
    $('prospect-rows').replaceChildren();
    for(const lead of leads){
      const row=node('tr');const name=node('td');name.append(node('strong',lead.name));if(lead.company)name.append(node('small',lead.company));
      const stage=node('td');stage.append(node('span',label(lead.stage),'status'));
      const match=node('td');match.append(node('span',lead.paid?'Paid invoice recorded':lead.registered?'Signed up via owner’s code':'Awaiting matching signup','status'));
      const actions=node('td');const group=node('div',undefined,'row-actions');group.append(button('Edit lead',()=>openLead(lead)),button('Add task',()=>openTask(null,lead)));actions.append(group);
      row.append(name,node('td',lead.email),node('td',lead.owner_email),stage,match,actions);$('prospect-rows').append(row);
    }
    if(!leads.length)empty('prospect-rows','No leads match these filters. Add a lead to start the next conversation.',6);
    $('prospect-page-info').textContent=`${leads.length} leads on this page · stage totals cover all matching leads`;
    $('prospect-next').disabled=!leadNext;$('prospect-first').hidden=!leadCursor;
  }
  async function loadLeads(){
    if(!team)return;const generation=++leadGeneration;$('pipeline-error').hidden=true;$('prospect-next').disabled=true;
    const query=new URLSearchParams({q:$('prospect-search').value,stage:$('prospect-stage').value,owner:$('prospect-owner').value,after:leadCursor});
    try{const data=await request(`leads?${query}`);if(generation!==leadGeneration)return;leads=data.leads||[];leadNext=data.next_cursor||'';renderStages(data.summary||[]);renderLeads();}
    catch(err){if(generation!==leadGeneration)return;leads=[];leadNext='';$('pipeline-summary').replaceChildren();empty('prospect-rows','Lead data is unavailable. Refresh data to try again.',6);error('pipeline-error',err);}
  }
  function renderTaskSummary(summary,on){
    const values=[['Open follow-ups',summary.open,'Across the selected owner scope'],['Due today',summary.today,dayLabel(on)],['Overdue',summary.overdue,'Open tasks needing your attention']];
    $('task-summary').replaceChildren(...values.map(([title,value,detail])=>{const card=node('article',undefined,'metric');card.append(node('span',title,'eyebrow'),node('strong',String(value)),node('small',detail));return card;}));
    $('task-reminder').textContent=`${summary.overdue} overdue · ${summary.today} due today. Dates follow your local calendar; reminders refresh while this page is visible.`;
    const count=summary.overdue+summary.today;$('task-nav-count').textContent=String(count);$('task-nav-count').hidden=!count;
  }
  function renderTasks(on){
    $('task-rows').replaceChildren();
    for(const task of tasks){
      const row=node('tr');if(task.done)row.className='task-complete';const title=node('td');title.append(node('strong',task.title),node('small',`${task.lead_name} · ${task.lead_email}`));
      const status=node('td');const overdue=!task.done&&task.due_on<on;status.append(node('span',task.done?'Completed':overdue?'Overdue':task.due_on===on?'Due today':'Upcoming',overdue?'status task-overdue':'status'));
      const actions=node('td');const group=node('div',undefined,'row-actions');group.append(button(task.done?'Reopen':'Complete',()=>toggleTask(task)),button('Edit task',()=>openTask(task,null)));actions.append(group);
      row.append(title,node('td',task.owner_email),node('td',dayLabel(task.due_on)),status,actions);$('task-rows').append(row);
    }
    if(!tasks.length)empty('task-rows','No tasks match these filters. Add a follow-up from a lead’s row.',5);
    $('task-page-info').textContent=`${tasks.length} tasks on this page · earliest due date first`;$('task-next').disabled=!taskNext;$('task-first').hidden=!taskCursor;
  }
  async function loadTasks(){
    if(!team)return;const generation=++taskGeneration;const on=today();if(lastDay!==on){taskCursor='';lastDay=on;}
    $('tasks-error').hidden=true;$('task-next').disabled=true;$('tasks-refresh').disabled=true;
    const query=new URLSearchParams({owner:$('task-owner').value,status:$('task-status').value,due:$('task-due').value,on,after:taskCursor});
    try{const data=await request(`tasks?${query}`);if(generation!==taskGeneration)return;tasks=data.tasks||[];taskNext=data.next_cursor||'';renderTaskSummary(data.summary,data.on);renderTasks(data.on);}
    catch(err){if(generation!==taskGeneration)return;tasks=[];taskNext='';$('task-summary').replaceChildren();$('task-nav-count').hidden=true;$('task-reminder').textContent='Task reminders are unavailable until data refreshes.';empty('task-rows','Task data is unavailable. Refresh tasks to try again.',5);error('tasks-error',err);}
    finally{if(generation===taskGeneration)$('tasks-refresh').disabled=false;}
  }
  const reload = () => Promise.all([loadLeads(),loadTasks()]);
  function openLead(lead){
    if(saving||!team)return;selectedLead=lead;$('prospect-dialog-title').textContent=lead?'Edit lead':'Add lead';
    $('prospect-name').value=lead?.name||'';$('prospect-email').value=lead?.email||'';$('prospect-company').value=lead?.company||'';$('prospect-note').value=lead?.note||'';
    options('prospect-edit-stage',team.stages.map(stage=>[stage,label(stage)]),lead?.stage||'new');
    const owners=ownerOptions();if(lead&&!owners.some(([id])=>id===lead.owner_id))owners.push([lead.owner_id,`${lead.owner_email} (unavailable — reassign)`]);
    options('prospect-edit-owner',owners,lead?.owner_id||team.actor_id);$('prospect-edit-owner').disabled=!team.manager;
    $('prospect-delete').hidden=!lead;$('prospect-form-error').textContent='';$('prospect-dialog').showModal();
  }
  function openTask(task,lead){
    if(saving||!team)return;selectedTask=task;taskLead=lead;$('task-dialog-title').textContent=task?'Edit follow-up task':'Add follow-up task';
    $('task-lead-name').textContent=task?`${task.lead_name} · ${task.lead_email}`:`${lead.name} · ${lead.email}`;
    $('task-title').value=task?.title||'';$('task-date').value=task?.due_on||today();$('task-done').checked=task?.done||false;$('task-done-label').hidden=!task;
    $('task-form-error').textContent='';$('task-dialog').showModal();
  }
  function pending(value){
    saving=value;for(const id of ['prospect-form','task-form'])for(const field of $(id).elements)field.disabled=value;
    if(!value&&team)$('prospect-edit-owner').disabled=!team.manager;
  }
  async function mutate(errorID,fn){
    if(saving)return;pending(true);$(errorID).textContent='';
    try{await fn();}catch(err){$(errorID).textContent=err.message;if($(errorID).hidden)$(errorID).hidden=false;}finally{pending(false);}
  }
  $('prospect-form').addEventListener('submit',event=>{
    event.preventDefault();const lead=selectedLead;
    const input={name:$('prospect-name').value,email:$('prospect-email').value,company:$('prospect-company').value,note:$('prospect-note').value,stage:$('prospect-edit-stage').value,owner_id:$('prospect-edit-owner').value,version:lead?.version||0};
    mutate('prospect-form-error',async()=>{await request(lead?`leads/${lead.id}`:'leads',{method:lead?'PUT':'POST',body:JSON.stringify(input)});$('prospect-dialog').close();$('pipeline-status').textContent='Lead saved.';await reload();});
  });
  $('prospect-delete').addEventListener('click',()=>{
    if(saving||!selectedLead||!window.confirm('Delete this lead and all its follow-up tasks? Referral records are kept.'))return;
    const lead=selectedLead;mutate('prospect-form-error',async()=>{await request(`leads/${lead.id}?version=${lead.version}`,{method:'DELETE'});$('prospect-dialog').close();$('pipeline-status').textContent='Lead and its tasks deleted.';await reload();});
  });
  $('task-form').addEventListener('submit',event=>{
    event.preventDefault();const task=selectedTask;const input={title:$('task-title').value,due_on:$('task-date').value,done:task?$('task-done').checked:false,version:task?.version||0};if(!task)input.lead_id=taskLead.id;
    mutate('task-form-error',async()=>{await request(task?`tasks/${task.id}`:'tasks',{method:task?'PUT':'POST',body:JSON.stringify(input)});$('task-dialog').close();await loadTasks();});
  });
  async function toggleTask(task){
    await mutate('tasks-error',async()=>{await request(`tasks/${task.id}`,{method:'PUT',body:JSON.stringify({title:task.title,due_on:task.due_on,done:!task.done,version:task.version})});await loadTasks();});
  }
  $('new-prospect').addEventListener('click',()=>openLead(null));
  for(const [close,dialog] of [['prospect-close','prospect-dialog'],['task-close','task-dialog']]){$(close).addEventListener('click',()=>{if(!saving)$(dialog).close();});$(dialog).addEventListener('cancel',event=>{if(saving)event.preventDefault();});}
  for(const id of ['prospect-stage','prospect-owner'])$(id).addEventListener('change',()=>{leadCursor='';loadLeads();});
  for(const id of ['task-status','task-due','task-owner'])$(id).addEventListener('change',()=>{taskCursor='';loadTasks();});
  let searchTimer;$('prospect-search').addEventListener('input',()=>{clearTimeout(searchTimer);searchTimer=setTimeout(()=>{leadCursor='';loadLeads();},250);});
  $('prospect-next').addEventListener('click',()=>{leadCursor=leadNext;loadLeads();});$('prospect-first').addEventListener('click',()=>{leadCursor='';loadLeads();});
  $('task-next').addEventListener('click',()=>{taskCursor=taskNext;loadTasks();});$('task-first').addEventListener('click',()=>{taskCursor='';loadTasks();});
  $('tasks-refresh').addEventListener('click',loadTasks);$('refresh').addEventListener('click',()=>{if(team)reload();else bootstrap();});
  window.addEventListener('focus',()=>{if(team&&!saving)loadTasks();});
  async function bootstrap(){
    try{
      team=await request('team');options('prospect-stage',team.stages.map(stage=>[stage,label(stage)]),'','All stages');
      for(const id of ['prospect-owner','task-owner'])options(id,ownerOptions(),'','All salespeople');
      $('prospect-owner-label').hidden=!team.manager;$('task-owner-label').hidden=!team.manager;$('new-prospect').disabled=false;
      await reload();clearInterval(poll);poll=setInterval(()=>{if(!document.hidden&&!saving&&!$('task-dialog').open&&!$('prospect-dialog').open)loadTasks();},team.refresh_seconds*1000);
    }catch(err){team=null;error('pipeline-error',err);error('tasks-error',err);empty('prospect-rows','The sales workspace is unavailable. Refresh data to try again.',6);empty('task-rows','The task inbox is unavailable.',5);}
  }
  bootstrap();
})();
