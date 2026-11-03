'use strict';
(() => {
  if (document.body.dataset.staff !== 'true') return;
  const $ = id => document.getElementById(id);
  const manager=document.body.dataset.manager==='true';
  let cursor='',next='',team=[],generation=0,busy=false;
  const reasons={no_reps:'No available salesperson',code_unavailable:'Code needs review',owner_conflict:'Code conflicts with existing owner',multiple_leads:'Multiple existing leads',owner_unavailable:'Existing owner unavailable',lead_unavailable:'Linked lead unavailable'};
  const node=(tag,text,cls)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;if(cls)n.className=cls;return n;};
  async function request(path,options={}) {
    const response=await fetch(`/v1/sales/${path}`,{credentials:'same-origin',cache:'no-store',...options,headers:{'Content-Type':'application/json'}});
    const data=await response.json().catch(()=>({}));
    if(!response.ok)throw new Error(data.detail||'Inquiries are unavailable. Refresh and try again.');
    return data;
  }
  function fail(err){$('inquiry-error').textContent=err.message;$('inquiry-error').hidden=false;}
  function button(text,fn){const b=node('button',text,'button secondary small');b.type='button';b.addEventListener('click',fn);return b;}
  async function change(row,action,owner) {
    if(busy)return;
    busy=true;const buttons=[...document.querySelectorAll('#inquiry-list button')].map(b=>[b,b.disabled]);buttons.forEach(([b])=>b.disabled=true);
    try{await request(`inquiries/${row.id}/${action}`,{method:'PUT',body:JSON.stringify({version:row.version,...(owner?{owner_id:owner}:{})})});await load();document.dispatchEvent(new Event('sales-inquiry-updated'));}
    catch(err){fail(err);}finally{busy=false;buttons.forEach(([b,disabled])=>b.disabled=disabled);}
  }
  function render(rows) {
    const cards=rows.map(row=>{
      const card=node('article',undefined,'panel inquiry-card');
      const heading=node('div',undefined,'section-heading');
      const title=node('div');title.append(node('span',row.intent==='demo'?'DEMO REQUEST':'CONTACT SALES','eyebrow'),node('h3',row.name));
      heading.append(title,node('span',row.status==='pending'?'Needs review':row.status==='closed'?'Closed':'Assigned','pill'));card.append(heading);
      card.append(node('p',[row.email,row.company].filter(Boolean).join(' · '),'muted'));
      const text=node('p',row.message,'inquiry-message');card.append(text);
      if(row.latest_message){card.append(node('strong',`Latest repeat message · ${row.repeat_count} repeat${row.repeat_count===1?'':'s'}`),node('p',row.latest_message,'inquiry-message'));}
      card.append(node('p',`Received ${new Date(row.last_received_at).toLocaleString()}${row.owner_email?' · Owner: '+row.owner_email:''}`,'muted'));
      if(row.salesperson_code)card.append(node('p',`Requested code: ${row.salesperson_code}`,'muted'));
      if(row.reason)card.append(node('p',reasons[row.reason]||'Manager review required','muted'));
      const actions=node('div',undefined,'inquiry-actions');
      if(manager && row.status==='pending') {
        const label=node('label','Assign to ');const select=node('select');select.setAttribute('aria-label',`Salesperson for ${row.email}`);
        select.append(new Option('Choose salesperson',''));team.forEach(rep=>select.append(new Option(rep.email,rep.id)));label.append(select);
        const assign=button('Assign & create follow-up',()=>change(row,'assignment',select.value));assign.disabled=true;select.addEventListener('change',()=>assign.disabled=!select.value);actions.append(label,assign);
      }
      if(row.status!=='closed')actions.append(button('Close inquiry',()=>{if(confirm('Close this inquiry? The lead and any follow-up tasks remain available.'))change(row,'close');}));
      if(row.lead_id){const link=node('a','Open lead pipeline','text-link');link.href='#pipeline';actions.append(link);}
      card.append(actions);return card;
    });
    $('inquiry-list').replaceChildren(...(cards.length?cards:[node('p','No inquiries match this view. Website requests will appear here.','muted')]));
    $('inquiry-status').textContent=`${rows.length} inquiries on this page · website contact details are unverified`;
    $('inquiry-next').disabled=!next;$('inquiry-first').hidden=!cursor;
  }
  async function load() {
    const current=++generation;$('inquiry-error').hidden=true;$('inquiry-next').disabled=true;
    try{const query=new URLSearchParams({status:$('inquiry-filter').value,after:cursor});const data=await request(`inquiries?${query}`);if(current!==generation)return;next=data.next_cursor||'';render(data.inquiries||[]);}
    catch(err){if(current!==generation)return;next='';$('inquiry-list').replaceChildren();$('inquiry-status').textContent='Inquiry data is unavailable.';fail(err);}
  }
  $('inquiry-filter').value=manager?'pending':'assigned';
  $('inquiry-filter').addEventListener('change',()=>{cursor='';load();});
  $('inquiry-refresh').addEventListener('click',()=>load());
  $('refresh').addEventListener('click',()=>load());
  $('inquiry-next').addEventListener('click',()=>{cursor=next;load();});
  $('inquiry-first').addEventListener('click',()=>{cursor='';load();});
  if(manager)request('team').then(data=>{team=data.members||[];return load();}).catch(fail);else load();
})();
