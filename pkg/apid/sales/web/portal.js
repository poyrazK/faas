'use strict';
(() => {
  const staff = document.body.dataset.staff === 'true';
  const base = staff ? '/v1/sales' : '/v1/referrals';
  const byId = id => document.getElementById(id);
  let snapshot = null;
  let cursor = new URLSearchParams(location.search).get('after') || '';
  let selected = null;
  let loading = false;
  const money = (cents, currency) => new Intl.NumberFormat(undefined, {style:'currency',currency:currency.toUpperCase()}).format(cents / 100);
  const date = value => value ? new Date(value).toLocaleDateString(undefined,{year:'numeric',month:'short',day:'numeric'}) : '—';
  const label = value => value.replaceAll('_',' ');
  function el(tag, text, className) {
    const node = document.createElement(tag);
    if (text !== undefined) node.textContent = text;
    if (className) node.className = className;
    return node;
  }
  async function request(path, options = {}) {
    const response = await fetch(path, {credentials:'same-origin',cache:'no-store',...options,headers:{'Content-Type':'application/json',...options.headers}});
    const data = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(data.detail || (response.status === 401 ? 'Your session expired. Sign in again through Account & security.' : 'The request could not be completed. Please try again.'));
    return data;
  }
  function showError(error) { byId('error').textContent = error.message; byId('error').hidden = false; }
  function renderMetrics() {
    const summary = snapshot.summary || [];
    const signups = summary.reduce((sum,row) => sum + row.signups,0);
    const paid = summary.reduce((sum,row) => sum + row.paid,0);
    const amounts = new Map();
    for (const row of summary) amounts.set(row.currency,(amounts.get(row.currency) || 0) + row.reward_cents);
    const amountsText = [...amounts].map(([currency,cents]) => money(cents,currency)).join(' · ') || money(0,snapshot.policy.currency);
    const values = [
      ['Referred signups',signups.toLocaleString(),'New accounts attributed to referrals'],
      ['Paid conversions',paid.toLocaleString(),'A recorded first invoice payment'],
      ['Signup → paid',signups ? `${(100*paid/signups).toFixed(1)}%` : '0%','Across all your attributed signups'],
      ['Eligible estimates',amountsText,'Payouts disabled · no funds issued']
    ];
    byId('metrics').replaceChildren(...values.map(([title,value,detail]) => {
      const card=el('article',undefined,'metric');card.append(el('span',title,'eyebrow'),el('strong',value),el('small',detail));return card;
    }));
  }
  function renderCode() {
    const hasCode = Boolean(snapshot.referral_code?.code);
    byId('referral-code').value = snapshot.referral_code?.code || '';
    byId('copy').disabled = !hasCode;
    byId('create-code').hidden = hasCode;
    byId('create-code').disabled = false;
    byId('toggle-code').hidden = !hasCode;
    byId('toggle-code').textContent = snapshot.referral_code?.active ? 'Pause code' : 'Resume code';
    byId('code-status').textContent = hasCode ? (snapshot.referral_code.active ? '● Active' : 'Paused for new signups') : '';
  }
  function filteredRows() {
    const query=(byId('search')?.value || '').toLowerCase();
    const stage=byId('stage')?.value || '';
    return (snapshot?.referrals || []).filter(row => (!query || row.email?.toLowerCase().includes(query) || row.note?.toLowerCase().includes(query)) && (!stage || row.stage === stage));
  }
  function renderRows() {
    const rows=filteredRows();
    byId('rows').replaceChildren();
    if (!rows.length) {
      const row=el('tr');const cell=el('td',snapshot.referrals.length ? 'No referrals match these filters on this page.' : 'No referrals yet. Share your code to start a new introduction.','empty');cell.colSpan=staff?7:4;row.append(cell);byId('rows').append(row);
    }
    rows.forEach((record,index) => {
      const row=el('tr');const customer=el('td');customer.append(el('strong',staff ? record.email : `Referral ${index+1}`));
      if (staff) customer.append(el('small',`${label(record.stage)} · ${record.kind === 'sales' ? 'Sales referral' : 'Customer referral'}`));
      row.append(customer,el('td',date(record.created_at)));
      if (staff) {
        const plan=el('td',label(record.plan));plan.append(el('small',`${record.apps || 0} apps · ${record.account_status}`));
        const follow=el('td',record.follow_up || 'Not scheduled');
        if (record.note) follow.append(el('small',record.note.length>60 ? `${record.note.slice(0,60)}…` : record.note));
        row.append(plan,follow);
      }
      const conversion=el('td');conversion.append(el('span',record.paid ? 'Invoice paid' : 'Registered','status'));
      const reward=el('td');reward.append(el('strong',money(record.reward_cents,record.currency)),el('small',label(record.reward_status)));
      if (record.eligible_at && ['eligible','holding'].includes(record.reward_status)) reward.append(el('small',`Hold ends ${date(record.eligible_at)}`));
      row.append(conversion,reward);
      if (staff) { const actions=el('td');const button=el('button','Follow up','text-button');button.type='button';button.addEventListener('click',()=>openLead(record));actions.append(button);row.append(actions); }
      byId('rows').append(row);
    });
    byId('page-info').textContent=`${rows.length} shown on this page · totals above cover all referrals`;
    byId('export').disabled=!rows.length;
    byId('next').disabled=!snapshot.next_cursor;
    byId('first').hidden=!cursor;
  }
  function render() {
    renderMetrics();renderCode();renderRows();
    byId('identity').textContent=snapshot.email;
    byId('scope-label').textContent=staff ? (snapshot.manager ? 'Manager view · all customer and sales referrals' : 'Your assigned sales referrals and customers') : 'Only signup and reward status are shared. Customer contact details stay private.';
    const p=snapshot.policy;
    byId('conversion-terms').textContent=`The first paid invoice must be recorded within ${p.conversion_days} days of signup. Currency must match ${p.currency.toUpperCase()}.`;
    const terms=staff ? `${p.commission_bps/100}% of the first invoice’s pre-tax paid amount.` : `${money(p.customer_reward_cents,p.currency)} for you; ${money(p.referred_reward_cents,p.currency)} for the new customer.`;
    byId('reward-terms').textContent=`${terms} ${p.hold_days}-day hold, verified email, and no refunds or pending refunds. New-signup terms shown; existing referrals retain their original terms. Payouts remain disabled.`;
    byId('observed').textContent=`Updated ${new Date(snapshot.observed_at).toLocaleString()}`;
  }
  async function load() {
    if (loading) return; loading=true;byId('refresh').disabled=true;byId('error').hidden=true;
    try { snapshot=await request(`${base}/me${cursor ? `?after=${encodeURIComponent(cursor)}` : ''}`);render(); }
    catch (error) { snapshot=null;byId('referral-code').value='';byId('copy').disabled=true;byId('export').disabled=true;byId('next').disabled=true;byId('identity').textContent='Session data unavailable';byId('metrics').replaceChildren();showError(error);byId('rows').replaceChildren();const row=el('tr');const cell=el('td','Referral data is unavailable. Use Refresh data to try again.','empty');cell.colSpan=7;row.append(cell);byId('rows').append(row); }
    finally { loading=false;byId('refresh').disabled=false; }
  }
  async function action(button, fn) {
    button.disabled=true;byId('error').hidden=true;
    try { await fn();await load(); } catch (error) { showError(error); } finally { button.disabled=false; }
  }
  byId('refresh').addEventListener('click',load);
  byId('create-code').addEventListener('click',event => action(event.currentTarget,()=>request(`${base}/codes`,{method:'POST',body:'{}'})));
  byId('toggle-code').addEventListener('click',event => action(event.currentTarget,()=>request(`${base}/codes/${encodeURIComponent(snapshot.referral_code.code)}`,{method:'PATCH',body:JSON.stringify({active:!snapshot.referral_code.active})})));
  byId('copy').addEventListener('click',async () => {
    try { await navigator.clipboard.writeText(byId('referral-code').value);byId('copy-status').textContent='Code copied. Ready to share.'; }
    catch { byId('referral-code').select();byId('copy-status').textContent='Select and copy the code from the field above.'; }
  });
  byId('next').addEventListener('click',()=>{cursor=snapshot.next_cursor;history.replaceState(null,'',`${location.pathname}?after=${encodeURIComponent(cursor)}#referrals`);load();});
  byId('first').addEventListener('click',()=>{cursor='';history.replaceState(null,'',`${location.pathname}#referrals`);load();});
  byId('search')?.addEventListener('input',renderRows);byId('stage')?.addEventListener('change',renderRows);
  const csvCell=value => { let text=String(value ?? '');if (/^[\s]*[=+\-@]/.test(text) || /^[\t\r\n]/.test(text)) text="'"+text;return '"'+text.replaceAll('"','""')+'"'; };
  byId('export').addEventListener('click',()=>{
    const headers=staff?['Customer','Joined','Plan','Stage','Follow-up','Paid','Reward status','Estimate cents','Currency','Notes']:['Joined','Paid','Reward status','Estimate cents','Currency'];
    const rows=filteredRows().map(r=>staff?[r.email,r.created_at,r.plan,r.stage,r.follow_up,r.paid,r.reward_status,r.reward_cents,r.currency,r.note]:[r.created_at,r.paid,r.reward_status,r.reward_cents,r.currency]);
    const blob=new Blob(['\uFEFF'+[headers,...rows].map(row=>row.map(csvCell).join(',')).join('\r\n')],{type:'text/csv;charset=utf-8'});
    const url=URL.createObjectURL(blob);const a=el('a');a.href=url;a.download='gregale-referrals-page.csv';a.click();setTimeout(()=>URL.revokeObjectURL(url),1000);
  });
  function openLead(record) {
    selected=record;byId('lead-email').textContent=record.email;byId('lead-stage').value=record.stage;byId('lead-follow-up').value=record.follow_up||'';byId('lead-note').value=record.note||'';byId('lead-error').textContent='';byId('lead-dialog').showModal();
  }
  byId('close-dialog')?.addEventListener('click',()=>byId('lead-dialog').close());
  byId('lead-form')?.addEventListener('submit',async event=>{
    event.preventDefault();const button=byId('save-lead');button.disabled=true;
    try { await request(`${base}/customers/${encodeURIComponent(selected.account_id)}`,{method:'PATCH',body:JSON.stringify({stage:byId('lead-stage').value,note:byId('lead-note').value,follow_up:byId('lead-follow-up').value})});byId('lead-dialog').close();await load(); }
    catch (error) { byId('lead-error').textContent=error.message; } finally { button.disabled=false; }
  });
  document.querySelectorAll('.nav-item[href^="#"]').forEach(link=>link.addEventListener('click',()=>{
    document.querySelectorAll('.nav-item').forEach(item=>{item.classList.remove('active');item.removeAttribute('aria-current');});link.classList.add('active');link.setAttribute('aria-current','location');
  }));
  load();
})();
