import {randomUUID} from 'node:crypto'
export function auditSettings(env){
 let admins;try{admins=JSON.parse(env.SUPPORT_AUDIT_ADMINS??'[]')}catch{throw new Error('Configure audit administrator subject/tenant pairs.')}
 const days=Number(env.SUPPORT_AUDIT_RETENTION_DAYS??7),limit=Number(env.SUPPORT_AUDIT_MAX_EVENTS??1000)
 if(!Array.isArray(admins)||admins.length>100||admins.some(a=>!a||Object.keys(a).some(k=>!['subject','tenant'].includes(k))||typeof a.subject!=='string'||!a.subject||a.subject.length>256||typeof a.tenant!=='string'||!a.tenant||a.tenant.length>256)||!Number.isInteger(days)||days<1||days>30||!Number.isInteger(limit)||limit<1||limit>10000)throw new Error('Configure bounded audit retention and administrators.')
 const url=env.SUPPORT_AUDIT_DATABASE_URL,outbox=env.SUPPORT_AUDIT_OUTBOX_DIR
 if(url){const endpoint=new URL(url);if(!['postgres:','postgresql:'].includes(endpoint.protocol)||!outbox||!outbox.startsWith('/'))throw new Error('Configure PostgreSQL audit URL and an absolute persistent outbox directory.')}
 return {admins,days,limit,url,outbox}
}
export function auditEvent({claims,action,status,resource},now=Date.now){
 if(!claims)return null
 const event={id:randomUUID(),time:new Date(now()).toISOString(),subject:claims.sub,tenant:claims.tenant,action,outcome:status<400?'success':status===401||status===403?'denied':'failed',status}
 if(resource)event.resource=resource
 return event
}
export function auditLog(config={},sink=()=>{},now=Date.now){
 const settings={admins:[],days:7,limit:1000,...config},events=[]
 const prune=()=>{const cutoff=now()-settings.days*86400000;while(events.length&&(Date.parse(events[0].time)<cutoff||events.length>settings.limit))events.shift()}
 return {
  admin:claims=>settings.admins.some(a=>a.subject===claims.sub&&a.tenant===claims.tenant),
  record(input){const event=auditEvent(input,now);return event?this.accept(event):false},
  accept(event){
   events.push({...event});prune()
   try{sink({...event})}catch{return false}
   return true
  },
  read(claims,filters={},limit=200){prune();return events.filter(e=>e.tenant===claims.tenant&&['subject','action','outcome'].every(k=>!filters[k]||e[k]===filters[k])&&(!filters.from||e.time>=filters.from)&&(!filters.to||e.time<=filters.to)&&(!filters.before||e.time<filters.before)).slice(-limit).reverse().map(e=>({...e}))}
 }
}
