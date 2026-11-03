'use strict';
(() => {
  if (document.body.dataset.manager !== 'true') return;
  const $=id=>document.getElementById(id);
  const el=(tag,text,cls)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;if(cls)n.className=cls;return n;};
  const stages=['new','contacted','qualified','demo','won','lost'];
  const browserZone=Intl.DateTimeFormat().resolvedOptions().timeZone||'UTC';
  const zone=()=>$('report-utc').checked?'UTC':browserZone;
  let snapshot=null,generation=0;
  const ratio=(n,d)=>d>0?n/d:null;
  const percent=value=>value===null?'—':`${(100*value).toFixed(1)}%`;
  const open=row=>row.leads.new+row.leads.contacted+row.leads.qualified+row.leads.demo;
  const duration=seconds=>{if(seconds===null)return '—';if(seconds<3600)return '<1h';const days=Math.floor(seconds/86400),hours=Math.floor(seconds%86400/3600);return days?`${days}d${hours?` ${hours}h`:''}`:`${hours}h`;};
  function money(cents,currency){
    const value=BigInt(cents),fraction=String(value%100n).padStart(2,'0');
    const parts=new Intl.NumberFormat(undefined,{minimumFractionDigits:2,maximumFractionDigits:2}).formatToParts(value/100n);
    return `${currency.toUpperCase()} ${parts.map(p=>p.type==='fraction'?fraction:p.value).join('')}`;
  }
  function dateString(date,utc){const y=utc?date.getUTCFullYear():date.getFullYear(),m=(utc?date.getUTCMonth():date.getMonth())+1,d=utc?date.getUTCDate():date.getDate();return `${y}-${String(m).padStart(2,'0')}-${String(d).padStart(2,'0')}`;}
  function shift(day,days){const d=new Date(`${day}T12:00:00Z`);d.setUTCDate(d.getUTCDate()+days);return dateString(d,true);}
  function preset(){const end=dateString(new Date(),$('report-utc').checked);$('report-to').value=end;$('report-from').value=$('report-preset').value==='month'?end.slice(0,7)+'-01':shift(end,-Number($('report-preset').value)+1);}
  function empty(id,text,columns){const row=el('tr'),cell=el('td',text,'empty');cell.colSpan=columns;row.append(cell);$(id).replaceChildren(row);}
  function clear(){snapshot=null;for(const id of ['report-metrics','report-stages','report-collections'])$(id).replaceChildren();$('report-payment-gap').hidden=true;$('report-export').disabled=true;$('report-money-export').disabled=true;empty('report-rows','No report data is available.',10);empty('report-money-rows','No collection data is available.',7);}
  function owners(){const selected=snapshot.window.owner;const select=$('report-owner');select.replaceChildren(new Option('All salespeople',''));for(const rep of snapshot.reps)select.append(new Option(rep.email+(rep.current_member?'':' · past/unavailable'),rep.id));if(selected&&!snapshot.reps.some(r=>r.id===selected))select.append(new Option('Unavailable salesperson',selected));select.value=selected;}
  function totals(){const t={leads:0,open:0,won:0,lost:0,signups:0,first:0};for(const row of snapshot.rows){t.leads+=row.leads.total;t.open+=open(row);t.won+=row.leads.won;t.lost+=row.leads.lost;t.signups+=row.signups;t.first+=row.first_payments;}return t;}
  function renderMetrics(){
    const t=totals();const cards=[['Leads created',t.leads,`${t.open} open · current owners`],['Won leads',t.won,`${percent(ratio(t.won,t.won+t.lost))} win rate across won/lost leads`],['Referred signups',t.signups,'Original referral credit'],['First payments recorded',t.first,`${percent(ratio(t.first,t.signups))} signup-to-payment rate`]];
    $('report-metrics').replaceChildren(...cards.map(([title,value,detail])=>{const card=el('article',undefined,'metric');card.append(el('span',title,'eyebrow'),el('strong',value.toLocaleString()),el('small',detail));return card;}));
    $('report-stages').replaceChildren(...stages.map(stage=>{const count=snapshot.rows.reduce((sum,row)=>sum+row.leads[stage],0);const card=el('div',undefined,'report-stage');const progress=el('progress');progress.max=Math.max(t.leads,1);progress.value=count;progress.setAttribute('aria-label',`${stage}: ${count}`);card.append(el('span',stage.charAt(0).toUpperCase()+stage.slice(1)),el('strong',count.toLocaleString()),progress);return card;}));
  }
  function sortedRows(){const sort=$('report-sort').value;const score=row=>sort==='leads'?row.leads.total:sort==='won'?row.leads.won:sort==='conversion'?(ratio(row.first_payments,row.signups)??-1):row.first_payments;return [...snapshot.rows].sort((a,b)=>sort==='email'?a.email.localeCompare(b.email):score(b)-score(a)||a.email.localeCompare(b.email));}
  function renderComparison(){
    $('report-rows').replaceChildren();for(const row of sortedRows()){const tr=el('tr'),who=el('td');who.append(el('strong',row.email),el('small',row.current_member?'Active team':'Past or unavailable'));tr.append(who,...[row.leads.total,open(row),row.leads.won,row.leads.lost,percent(ratio(row.leads.won,row.leads.won+row.leads.lost)),row.signups,row.first_payments,percent(ratio(row.first_payments,row.signups)),duration(row.avg_first_payment_seconds)].map(value=>el('td',typeof value==='number'?value.toLocaleString():value)));$('report-rows').append(tr);}
    if(!snapshot.rows.length)empty('report-rows','No salespeople match this selection.',10);
  }
  function renderCollections(){
    const reps=new Map(snapshot.reps.map(r=>[r.id,r.email]));const sums=new Map();$('report-money-rows').replaceChildren();
    for(const row of snapshot.collections){
      const key=row.currency;const sum=sums.get(key)||{invoices:0,paid:0n,refund:0n,pending:0n,retained:0n};sum.invoices+=row.invoices;sum.paid+=BigInt(row.paid_cents);sum.refund+=BigInt(row.refunded_cents);sum.pending+=BigInt(row.pending_refund_cents);sum.retained+=BigInt(row.retained_cents);sums.set(key,sum);
      const tr=el('tr');tr.append(el('td',reps.get(row.owner_id)||'Unavailable salesperson'),el('td',row.currency.toUpperCase()),el('td',row.invoices.toLocaleString()),...['paid_cents','refunded_cents','pending_refund_cents','retained_cents'].map(field=>el('td',money(row[field],row.currency))));$('report-money-rows').append(tr);
    }
    $('report-collections').replaceChildren(...[...sums].sort(([a],[b])=>a.localeCompare(b)).map(([currency,s])=>{const card=el('article',undefined,'panel collection-card');card.append(el('span',`${currency.toUpperCase()} · NET FIRST-INVOICE COLLECTIONS`,'eyebrow'),el('strong',money(s.retained,currency)),el('p',`${s.invoices} first invoices · paid ${money(s.paid,currency)}`,'muted'),el('p',`Refunded ${money(s.refund,currency)} · pending ${money(s.pending,currency)}`,'muted'));return card;}));
    if(!snapshot.collections.length)empty('report-money-rows','No matching first-invoice payments have been recorded for these signup dates.',7);
    const missing=snapshot.rows.reduce((sum,row)=>sum+row.missing_invoices,0);$('report-payment-gap').hidden=!missing;$('report-payment-gap').textContent=`${missing} first-payment records have no matching invoice snapshot. Their amounts are excluded from collections.`;
  }
  function validate(data){if(!data.window||!data.observed_at||!Array.isArray(data.rows)||!Array.isArray(data.reps)||!Array.isArray(data.collections))throw new Error('The report response is unavailable. Refresh and try again.');for(const r of data.collections)for(const field of ['paid_cents','refunded_cents','pending_refund_cents','retained_cents'])if(typeof r[field]!=='string'||!/^\d+$/.test(r[field]))throw new Error('Collection totals are unavailable. Refresh and try again.');return data;}
  function pending(value){for(const field of $('report-filters').elements)field.disabled=value;}
  async function load(){
    const current=++generation;clear();$('report-error').hidden=true;pending(true);$('report-period').textContent='Loading the team report…';
    const query=new URLSearchParams({from:$('report-from').value,to:$('report-to').value,tz:zone(),owner:$('report-owner').value});
    try{
      const response=await fetch(`/v1/sales/reports?${query}`,{credentials:'same-origin',cache:'no-store'});const data=await response.json().catch(()=>({}));if(current!==generation)return;if(!response.ok)throw new Error(data.detail||'The report is unavailable. Sign in and try again.');snapshot=validate(data);
      owners();renderMetrics();renderComparison();renderCollections();const w=snapshot.window;const observed=new Intl.DateTimeFormat(undefined,{dateStyle:'medium',timeStyle:'short',timeZone:w.timezone}).format(new Date(snapshot.observed_at));$('report-period').textContent=`${w.from} through ${w.to} · ${w.timezone} · observed ${observed}`;$('report-export').disabled=!snapshot.rows.length;$('report-money-export').disabled=!snapshot.collections.length;
    }catch(error){if(current!==generation)return;clear();$('report-error').textContent=error.message;$('report-error').hidden=false;$('report-period').textContent='Report data is unavailable. Apply filters to retry.';}
    finally{if(current===generation)pending(false);}
  }
  function csvCell(value){let text=String(value??'');if(/^[\s]*[=+\-@]/.test(text)||/^[\t\r\n]/.test(text))text="'"+text;return '"'+text.replaceAll('"','""')+'"';}
  function download(kind,headers,rows){const w=snapshot.window;const prefix=[w.from,w.to,w.timezone,snapshot.observed_at];const text='\uFEFF'+[['From','Through','Time zone','Observed at',...headers],...rows.map(row=>[...prefix,...row])].map(row=>row.map(csvCell).join(',')).join('\r\n');const url=URL.createObjectURL(new Blob([text],{type:'text/csv;charset=utf-8'}));const a=el('a');a.href=url;a.download=`gregale-sales-${kind}-${w.from}-${w.to}.csv`;a.click();setTimeout(()=>URL.revokeObjectURL(url),1000);}
  const percentCSV=(n,d)=>d>0?(100*n/d).toFixed(1):'';
  $('report-export').addEventListener('click',()=>{if(!snapshot)return;download('comparison',['Salesperson','Active team','Leads','Open','Won','Lost','Win rate percent','Referred signups','First payments','Signup to payment percent','Average seconds to first payment'],sortedRows().map(r=>[r.email,r.current_member,r.leads.total,open(r),r.leads.won,r.leads.lost,percentCSV(r.leads.won,r.leads.won+r.leads.lost),r.signups,r.first_payments,percentCSV(r.first_payments,r.signups),r.avg_first_payment_seconds??'']));});
  $('report-money-export').addEventListener('click',()=>{if(!snapshot)return;const reps=new Map(snapshot.reps.map(r=>[r.id,r.email]));download('collections',['Salesperson','Currency','First invoices','Paid cents','Refunded cents','Pending refund cents','Retained cents'],snapshot.collections.map(r=>[reps.get(r.owner_id)||'Unavailable salesperson',r.currency,r.invoices,r.paid_cents,r.refunded_cents,r.pending_refund_cents,r.retained_cents]));});
  $('report-filters').addEventListener('submit',event=>{event.preventDefault();load();});$('report-preset').addEventListener('change',()=>{if($('report-preset').value!=='custom'){preset();load();}});$('report-owner').addEventListener('change',load);$('report-utc').addEventListener('change',()=>{if($('report-preset').value!=='custom')preset();load();});
  for(const id of ['report-from','report-to'])$(id).addEventListener('change',()=>{$('report-preset').value='custom';$('report-export').disabled=true;$('report-money-export').disabled=true;});
  $('report-sort').addEventListener('change',()=>{if(snapshot)renderComparison();});$('refresh').addEventListener('click',load);
  preset();load();
})();
