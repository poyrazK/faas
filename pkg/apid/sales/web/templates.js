'use strict';
(() => {
  if(document.body.dataset.staff!=='true')return;
  const $=id=>document.getElementById(id),manager=document.body.dataset.manager==='true';
  const labels={intro:'Introduction',demo:'Demo invitation',follow_up:'Follow-up',stalled:'Stalled deal'};
  const fields=[['lead_name','Lead name'],['lead_email','Lead email'],['company','Company'],['salesperson_email','Salesperson email'],['referral_code','Referral code']];
  const node=(tag,text,cls)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;if(cls)n.className=cls;return n;};
  const button=(text,fn)=>{const b=node('button',text,'text-button');b.type='button';b.addEventListener('click',fn);return b;};
  async function request(path,options={}){
    const r=await fetch(`/v1/sales/${path}`,{credentials:'same-origin',cache:'no-store',...options,headers:{'Content-Type':'application/json',...options.headers}});
    const data=await r.json().catch(()=>({}));if(!r.ok){const e=new Error(data.detail||'Templates are unavailable. Try again.');e.code=data.code;throw e;}return data;
  }
  function error(id,e){$(id).textContent=e.message;$(id).hidden=false;}
  let catalog=[],cursor='',next='',catalogGeneration=0,selected=null,reviewed=null,saving=false;
  async function loadCatalog(){
    const generation=++catalogGeneration;$('templates-error').hidden=true;$('templates-status').textContent='Loading templates…';$('templates-next').disabled=true;
    try{
      const d=await request(`templates?after=${encodeURIComponent(cursor)}`);if(generation!==catalogGeneration)return;catalog=d.templates;next=d.next_cursor;
      const rows=catalog.map(t=>{const row=node('tr'),title=node('td',t.title);title.append(node('small',`Revision ${t.version}`));row.append(title,node('td',labels[t.category]),node('td',t.stage||'Any stage'),node('td',t.status));if(manager){const action=node('td');action.append(button('Edit template',()=>openEditor(t)));row.append(action);}return row;});
      if(!rows.length){const row=node('tr'),cell=node('td',manager?'No templates yet. Create a draft to get started.':'No active templates yet. Ask your sales manager to add one.','empty');cell.colSpan=manager?5:4;row.append(cell);rows.push(row);}
      $('template-rows').replaceChildren(...rows);$('templates-status').textContent=manager?'Drafts and archives are visible to managers. Active messages are available from each lead.':'Team-approved messages. Choose Templates on a lead to personalize and copy.';$('templates-next').disabled=!next;$('templates-first').hidden=!cursor;
    }catch(e){if(generation===catalogGeneration){error('templates-error',e);$('templates-status').textContent='Templates could not be refreshed.';}}
  }
  $('templates-refresh').addEventListener('click',loadCatalog);$('templates-first').addEventListener('click',()=>{cursor='';loadCatalog();});$('templates-next').addEventListener('click',()=>{cursor=next;loadCatalog();});
  function editorPending(value){saving=value;for(const control of $('template-form').elements)control.disabled=value;}
  function openEditor(t){
    if(saving)return;selected=t;reviewed=null;$('template-editor-title').textContent=t?'Edit message template':'New message template';
    for(const key of ['title','category','stage','status','subject','body'])$(`template-${key}`).value=t?.[key]??({category:'intro',status:'draft'}[key]||'');
    $('template-form-error').textContent='';$('template-latest').hidden=true;$('template-review-latest').hidden=true;$('template-replace').hidden=true;$('template-editor').showModal();
  }
  async function reviewLatest(){
    editorPending(true);try{reviewed=await request(`templates/${selected.id}`);const panel=$('template-latest');panel.replaceChildren(node('strong',`Saved revision ${reviewed.version} · ${reviewed.status}`),node('p',`${reviewed.title} · ${labels[reviewed.category]} · ${reviewed.stage||'Any stage'}`),node('p',reviewed.subject||'No subject'),node('pre',reviewed.body));panel.hidden=false;$('template-replace').hidden=false;$('template-form-error').textContent='Your draft is preserved above. Review the saved version before replacing it.';}catch(e){$('template-form-error').textContent=e.message;}finally{editorPending(false);}
  }
  async function saveTemplate(replace=false){
    if(saving)return;const inData={version:(replace?reviewed:selected)?.version||0};for(const key of ['title','category','stage','status','subject','body'])inData[key]=$(`template-${key}`).value;
    editorPending(true);$('template-form-error').textContent='';try{await request(selected?`templates/${selected.id}`:'templates',{method:selected?'PUT':'POST',body:JSON.stringify(inData)});$('template-editor').close();loadCatalog();invalidatePreview('Templates changed. Refresh the preview before copying.');}catch(e){$('template-form-error').textContent=e.message;if(e.code==='sales_record_conflict'){$('template-review-latest').hidden=false;$('template-replace').hidden=true;reviewed=null;}}finally{editorPending(false);}
  }
  if(manager){
    $('template-new').addEventListener('click',()=>openEditor(null));$('template-editor-close').addEventListener('click',()=>{if(!saving)$('template-editor').close();});$('template-editor').addEventListener('cancel',e=>{if(saving)e.preventDefault();});
    $('template-form').addEventListener('submit',e=>{e.preventDefault();saveTemplate();});$('template-review-latest').addEventListener('click',reviewLatest);$('template-replace').addEventListener('click',()=>{if(reviewed&&$('template-form').reportValidity())saveTemplate(true);});
    for(const [key,label] of fields){const b=button(`+ ${label}`,()=>{const area=$('template-body');area.setRangeText(`{{${key}}}`,area.selectionStart,area.selectionEnd,'end');area.focus();});b.className='button secondary small';b.title=`Insert {{${key}}}`;$('template-fields').append(b);}
  }
  let leadId='',choices=[],choiceCursor='',choiceNext='',preview=null,messageGeneration=0,choiceGeneration=0,copying=false;
  function invalidatePreview(text=''){
    ++messageGeneration;preview=null;$('message-copy').disabled=true;$('message-copy-status').textContent='';$('message-subject').value='';$('message-body').value='';$('message-missing').hidden=true;$('message-status').textContent=text;
  }
  async function loadChoices(){
    const generation=++choiceGeneration;invalidatePreview('Choose a template to prepare your message.');$('message-error').hidden=true;$('message-template').disabled=true;$('message-next').disabled=true;
    try{const d=await request(`templates?after=${encodeURIComponent(choiceCursor)}`);if(generation!==choiceGeneration)return;choices=d.templates;choiceNext=d.next_cursor;$('message-template').replaceChildren(new Option(choices.length?'Choose a template':'No templates on this page',''),...choices.map(t=>new Option(`${t.title}${t.status!=='active'?` · ${t.status}`:''} · ${t.stage||'Any stage'}`,t.id)));$('message-template').disabled=!choices.length;$('message-next').disabled=!choiceNext;$('message-first').hidden=!choiceCursor;if(!choices.length)$('message-status').textContent='Ask your manager to activate a message template.';}catch(e){if(generation===choiceGeneration)error('message-error',e);}
  }
  function renderPreview(d){
    preview=d;$('message-lead').textContent=`${d.lead.name} · ${d.lead.company||d.lead.email}`;$('message-owner').textContent=`Assigned salesperson: ${d.lead.owner_email} · ${d.lead.stage}`;$('message-subject').value=d.message.subject;$('message-body').value=d.message.body;
    const missing=d.message.missing.map(k=>fields.find(f=>f[0]===k)?.[1]||k);$('message-missing').hidden=!missing.length;$('message-missing').textContent=`Missing: ${missing.join(', ')}. Update the lead or choose a template without these fields. Referral codes must be active and belong to an eligible assigned salesperson.`;
    $('message-status').textContent=`Revision ${d.template.version} · ${d.template.status}${d.template.stage&&d.template.stage!==d.lead.stage?' · Suggested for a different lead stage':''}. Review this message before using it.`;$('message-copy').disabled=!!missing.length||copying;$('message-copy-status').textContent='';
  }
  async function fetchPreview(){const t=choices.find(t=>t.id===$('message-template').value);if(!t)return null;return request(`leads/${leadId}/templates/${t.id}/preview?version=${t.version}`);}
  async function refreshPreview(){
    invalidatePreview('Loading personalized message…');$('message-error').hidden=true;const generation=messageGeneration;
    try{const d=await fetchPreview();if(generation!==messageGeneration)return;if(d)renderPreview(d);else $('message-status').textContent='Choose a template to prepare your message.';}catch(e){if(generation===messageGeneration){error('message-error',e);$('message-status').textContent='Refresh the template list if this version changed.';}}
  }
  document.addEventListener('sales-open-template',e=>{if(!e.detail?.leadId)return;leadId=e.detail.leadId;choiceCursor='';$('message-lead').textContent='Prepare a message for this lead';$('message-owner').textContent='';$('message-dialog').showModal();loadChoices();});
  $('message-template').addEventListener('change',refreshPreview);$('message-next').addEventListener('click',()=>{choiceCursor=choiceNext;loadChoices();});$('message-first').addEventListener('click',()=>{choiceCursor='';loadChoices();});
  $('message-refresh').addEventListener('click',async()=>{const id=$('message-template').value;if(!id){loadChoices();return;}invalidatePreview();const generation=messageGeneration;try{const current=await request(`templates/${id}`);if(generation!==messageGeneration)return;const at=choices.findIndex(t=>t.id===id);if(at>=0)choices[at]=current;await refreshPreview();}catch(e){error('message-error',e);}});
  $('message-close').addEventListener('click',()=>$('message-dialog').close());$('message-dialog').addEventListener('close',()=>{++choiceGeneration;invalidatePreview();});
  $('message-copy').addEventListener('click',async()=>{
    if(!preview||copying)return;const original=preview,generation=messageGeneration;copying=true;$('message-copy').disabled=true;$('message-error').hidden=true;
    try{
      const fresh=await fetchPreview();if(generation!==messageGeneration)return;
      if(!fresh||fresh.message.missing.length||fresh.lead.version!==original.lead.version||fresh.message.subject!==original.message.subject||fresh.message.body!==original.message.body){if(fresh)renderPreview(fresh);$('message-copy-status').textContent='Details changed. Review the refreshed message before copying.';return;}
      const text=(fresh.message.subject?`Subject: ${fresh.message.subject}\n\n`:'')+fresh.message.body;
      try{await navigator.clipboard.writeText(text);}catch{if(generation===messageGeneration)$('message-copy-status').textContent='Clipboard access is unavailable. Select and copy the subject and message above.';return;}if(generation===messageGeneration)$('message-copy-status').textContent='Message copied.';
    }catch(e){if(generation===messageGeneration){invalidatePreview();error('message-error',e.code?e:new Error('Copy failed. Refresh the preview and try again.'));}}finally{copying=false;if(generation===messageGeneration)$('message-copy').disabled=!preview||!!preview.message.missing.length;}
  });
  document.addEventListener('sales-pipeline-updated',()=>{if($('message-dialog').open)invalidatePreview('Lead details changed. Refresh the preview before copying.');});
  loadCatalog();
})();
