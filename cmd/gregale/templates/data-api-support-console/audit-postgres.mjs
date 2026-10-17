import {mkdir,readdir,readFile,writeFile,rename,unlink,open} from 'node:fs/promises'
import {join} from 'node:path'
export function auditFilters(params){
 const value={}
 for(const key of params.keys())if(!['subject','action','outcome','from','to','before','format'].includes(key)||params.getAll(key).length!==1)throw new Error('Unsupported audit filter.')
 for(const key of ['subject','action','outcome'])if(params.has(key)){const text=params.get(key);if(!text||text.length>256||/[\x00-\x1f\x7f]/.test(text))throw new Error('Invalid audit filter.');value[key]=text}
 if(value.outcome&&!['success','failed','denied'].includes(value.outcome))throw new Error('Invalid audit outcome.')
 for(const key of ['from','to','before'])if(params.has(key)){const text=params.get(key);if(!/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d{1,3})?Z$/.test(text)||!Number.isFinite(Date.parse(text)))throw new Error('Use UTC ISO audit dates.');value[key]=new Date(text).toISOString()}
 if(value.from&&value.to&&value.from>value.to)throw new Error('Invalid date range.')
 if(params.has('format')&&params.get('format')!=='csv')throw new Error('Invalid audit format.')
 return value
}
export const auditColumns=['id','time','subject','tenant','action','resource','outcome','status']
export function auditCSV(events){
 const cell=value=>{const text=String(value??'');return '"'+(/^[\s\uFEFF]*[=+\-@]|^[\t\r\n]/.test(text)?"'"+text:text).replaceAll('"','""')+'"'}
 return '\uFEFF'+auditColumns.map(cell).join(',')+'\r\n'+events.map(e=>auditColumns.map(c=>cell(e[c])).join(',')).join('\r\n')+'\r\n'
}
export async function postgresAudit({url,outbox,days},options={}){
 const pool=options.pool??new (await import('pg')).default.Pool({connectionString:url,max:2,connectionTimeoutMillis:3000,query_timeout:5000,statement_timeout:5000})
 let closed=false,running=false,timer=null,lastFailure=null,writing=0
 // An idle client dropped by a database restart must not crash the console; the outbox retries delivery.
 if(!options.pool)pool.on('error',()=>{lastFailure=new Date().toISOString()})
 const pending=new Map()
 try{
 const privilege=await pool.query("SELECT r.rolsuper,r.rolbypassrls,c.relowner=r.oid AS owner,c.relrowsecurity,c.relforcerowsecurity FROM pg_roles r JOIN pg_class c ON c.oid='support_audit.events'::regclass WHERE r.rolname=current_user")
 if(!privilege.rows.length||privilege.rows[0].rolsuper||privilege.rows[0].rolbypassrls||privilege.rows[0].owner||!privilege.rows[0].relrowsecurity||!privilege.rows[0].relforcerowsecurity){throw new Error('Use a non-owner, non-superuser audit database login.')}
 await mkdir(outbox,{recursive:true,mode:0o700})
 for(const name of await readdir(outbox)){
  if(!/^[a-f0-9-]{36}\.json$/.test(name))continue
  if(pending.size>=10000)throw new Error('Audit outbox exceeds bounds.')
  const text=await readFile(join(outbox,name),'utf8');if(text.length>4096)throw new Error('Invalid audit outbox.')
  const event=JSON.parse(text);if(event.id+'.json'!==name)throw new Error('Invalid audit outbox ID.')
  pending.set(event.id,event)
 }
 }catch(error){await pool.end();throw error}
 const transaction=async(tenant,fn)=>{
  const client=await pool.connect()
  try{await client.query('BEGIN');await client.query("SELECT set_config('support_audit.tenant',$1,true)",[tenant]);const value=await fn(client);await client.query('COMMIT');return value}catch(error){await client.query('ROLLBACK').catch(()=>{});throw error}finally{client.release()}
 }
 const cleanup=client=>client.query("DELETE FROM support_audit.events WHERE time < now()-($1::int * interval '1 day')",[days])
 async function flush(){
  if(closed||running)return;running=true
  try{
   for(const event of pending.values()){
    await transaction(event.tenant,async client=>{
     await client.query('INSERT INTO support_audit.events(id,time,subject,tenant,action,resource,outcome,status) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(id) DO NOTHING',auditColumns.map(c=>event[c]??null))
     await cleanup(client)
    })
    await unlink(join(outbox,event.id+'.json'));pending.delete(event.id)
   }
   lastFailure=null
  }catch{lastFailure=new Date().toISOString()}finally{running=false}
 }
 timer=setInterval(()=>void flush(),2000);timer.unref();void flush()
 return {
  async record(event){
   if(closed||pending.size+writing>=10000)return false
   writing++
   const file=join(outbox,event.id+'.json'),temp=file+'.tmp'
   try{await writeFile(temp,JSON.stringify(event),{mode:0o600,flush:true});await rename(temp,file);const directory=await open(outbox,"r");try{await directory.sync()}finally{await directory.close()}pending.set(event.id,event);void flush();return true}catch{lastFailure=new Date().toISOString();return false}finally{writing--}
  },
  async read(claims,filters={},limit=200){
   return transaction(claims.tenant,async client=>{
    await cleanup(client)
    const args=[claims.tenant],where=['tenant=$1']
    for(const key of ['subject','action','outcome'])if(filters[key]){args.push(filters[key]);where.push(key+'=$'+args.length)}
    for(const [key,op]of [['from','>='],['to','<='],['before','<']])if(filters[key]){args.push(filters[key]);where.push('time'+op+'$'+args.length+'::timestamptz')}
    args.push(limit)
    const result=await client.query('SELECT id,time,subject,tenant,action,resource,outcome,status FROM support_audit.events WHERE '+where.join(' AND ')+' ORDER BY time DESC,id DESC LIMIT $'+args.length,args)
    return result.rows.map(row=>({...row,time:row.time.toISOString()}))
   })
  },
  status:()=>({mode:'postgres',pending:pending.size,last_failure:lastFailure}),
  flush,
  async close(){closed=true;clearInterval(timer);while(running)await new Promise(resolve=>setTimeout(resolve,20));await pool.end()}
 }
}
