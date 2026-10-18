export const historyLimits=Object.freeze({runs:20,bytes:2097152,days:365,sessionBytes:10485760})
export const emptyHistory=()=>({version:1,retention:{runs:20,days:30},reports:[]})
const fields=(v,allowed)=>v&&typeof v==='object'&&!Array.isArray(v)&&Object.keys(v).every(key=>allowed.includes(key))
const uuid=v=>typeof v==='string'&&/^[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}$/i.test(v)
const outcomes=['confirmed','refused','conflict','uncertain','not_sent']
const equal=(a,b)=>JSON.stringify(a)===JSON.stringify(b)
export function validateBulkReport(r){
 const fail=()=>{throw new Error('Use a valid redacted bulk-operation report.')}
 if(!fields(r,['version','run_id','retry_of','reconciliation_of','operation','source','schema','table','identity','fingerprint','started_at','finished_at','termination','total','summary','rows','reconciliation','retryable'])||r.version!==1||!uuid(r.run_id)||!['update','delete'].includes(r.operation)||!['selected','matching'].includes(r.source)||!['completed','cancelled','context_changed','stopped'].includes(r.termination)||!['schema','table','identity'].every(key=>typeof r[key]==='string'&&r[key].length>0&&r[key].length<=256)||!/^[a-f0-9]{64}$/.test(r.fingerprint)||!Number.isInteger(r.total)||r.total<1||r.total>1000||!Array.isArray(r.rows)||r.rows.length!==r.total)fail()
 for(const key of ['started_at','finished_at'])if(typeof r[key]!=='string'||!Number.isFinite(Date.parse(r[key]))||new Date(r[key]).toISOString()!==r[key])fail()
 if(Date.parse(r.finished_at)<Date.parse(r.started_at))fail()
 const parent=r.retry_of??r.reconciliation_of,hasParent='retry_of'in r||'reconciliation_of'in r
 if('retry_of'in r&&'reconciliation_of'in r||hasParent&&(!uuid(parent)||parent===r.run_id))fail()
 const sources=new Set()
 r.rows.forEach((row,index)=>{
  if(!fields(row,['index','outcome','status','source_index'])||row.index!==index+1||!outcomes.includes(row.outcome)||'status'in row&&(!Number.isInteger(row.status)||row.status<100||row.status>599))fail()
  if(hasParent){if(!Number.isInteger(row.source_index)||row.source_index<1||row.source_index>1000||sources.has(row.source_index))fail();sources.add(row.source_index)}else if('source_index'in row)fail()
 })
 if(!fields(r.summary,outcomes)||outcomes.some(outcome=>r.summary[outcome]!==r.rows.filter(row=>row.outcome===outcome).length))fail()
 if(!fields(r.reconciliation,['conflicts','uncertain'])||!equal(r.reconciliation.conflicts,r.rows.filter(row=>row.outcome==='conflict').map(row=>row.index))||!equal(r.reconciliation.uncertain,r.rows.filter(row=>row.outcome==='uncertain').map(row=>row.index))||!equal(r.retryable,r.rows.filter(row=>['refused','not_sent'].includes(row.outcome)).map(row=>row.index)))fail()
 return r
}
export function validateBulkHistory(value){
 const fail=()=>{throw new Error('Use a bounded redacted operation history.')}
 if(!fields(value,['version','retention','reports'])||value.version!==1||!fields(value.retention,['runs','days'])||!Number.isInteger(value.retention.runs)||value.retention.runs<1||value.retention.runs>historyLimits.runs||!Number.isInteger(value.retention.days)||value.retention.days<1||value.retention.days>historyLimits.days||!Array.isArray(value.reports)||value.reports.length>value.retention.runs)fail()
 const ids=new Set();for(const report of value.reports){validateBulkReport(report);if(ids.has(report.run_id))fail();ids.add(report.run_id)}
 if(new TextEncoder().encode(JSON.stringify(value,null,2)+'\n').length>historyLimits.bytes)fail()
 return value
}
export function retainBulkHistory(value,report=null,now=Date.now()){
 validateBulkHistory(value);if(report)validateBulkReport(report)
 const cutoff=now-value.retention.days*86400000
 const reports=[...(report?[report]:[]),...value.reports.filter(item=>item.run_id!==report?.run_id)].filter(item=>Date.parse(item.finished_at)>=cutoff).sort((a,b)=>Date.parse(b.finished_at)-Date.parse(a.finished_at)).slice(0,value.retention.runs)
 const next={version:1,retention:{...value.retention},reports};while(new TextEncoder().encode(JSON.stringify(next,null,2)+'\n').length>historyLimits.bytes&&reports.length)reports.pop()
 return validateBulkHistory(next)
}
