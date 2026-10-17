import {createHash} from 'node:crypto'
import {dataTarget} from './queries.mjs'
export function validateActions(definition){
 if(definition.actions===undefined)return
 if(!Array.isArray(definition.actions)||definition.actions.length>6)throw new Error('Configure at most six approved support actions.')
 const ids=new Set()
 for(const a of definition.actions){
  const r=definition.resources.find(r=>r.id===a.resource)
  if(!['add_reply','close_case','assign_case','unassign_case','set_priority','add_note'].includes(a.id)||ids.has(a.id)||!r||!r.columns.includes(a.version)||!r.keys.includes(a.idColumn)||typeof a.label!=='string'||!a.label||a.label.length>80)throw new Error('Configure approved support action resources, case IDs and version columns.')
  ids.add(a.id)
 }
}
export function permissions(claims,definition){
 const signed=claims.gregale_support_actions
 return Array.isArray(signed)&&signed.every(v=>typeof v==='string')?(definition.actions??[]).filter(a=>signed.includes(a.id)&&(a.id!=='add_note'||claims.gregale_internal_notes===true)).map(a=>a.id):[]
}
export function actionInput(value,claims,definition){
 if(!value||Object.keys(value).some(k=>!['action','keys','body'].includes(k)))throw new Error('Unsupported action input.')
 const action=definition.actions?.find(a=>a.id===value.action)
 if(!action||!permissions(claims,definition).includes(action.id))throw Object.assign(new Error('Support action permission required.'),{status:403})
 const r=definition.resources.find(r=>r.id===action.resource)
 if(!Array.isArray(value.keys)||value.keys.length!==r.keys.length||value.keys.some(v=>typeof v==='string'&&(v.length>256||/[\x00-\x1f\x7f]/.test(v))||!['string','number','boolean'].includes(typeof v)||typeof v==='number'&&!Number.isSafeInteger(v))||['add_reply','add_note'].includes(action.id)&&(typeof value.body!=='string'||!value.body.trim()||value.body.length>1000||/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/.test(value.body))||action.id==='set_priority'&&!['low','normal','high','urgent'].includes(value.body)||!['add_reply','add_note','set_priority'].includes(action.id)&&value.body!==undefined)throw new Error('Invalid support action fields.')
 return {action,r,input:{action:action.id,keys:value.keys,...(['add_reply','add_note','set_priority'].includes(action.id)?{body:value.body}:{})}}
}
export function previewQuery(config,definition,action,r,keys){
 const query=dataTarget(config,definition,new URLSearchParams({resource:r.id,limit:'2'}))
 r.keys.forEach((column,i)=>query.target.searchParams.append(column,'eq.'+String(keys[i])))
 return query
}
export const sessionBinding=token=>createHash('sha256').update(token).digest('hex')
export async function actionDatabase(url){
 const {default:pg}=await import('pg'),pool=new pg.Pool({connectionString:url,max:2,connectionTimeoutMillis:3000,query_timeout:5000,statement_timeout:5000})
 // An idle client dropped by a database restart must not crash the console; driver errors can carry connection details.
 pool.on('error',()=>console.error('support action database connection unavailable'))
 try{const {rows}=await pool.query("SELECT r.rolsuper,r.rolbypassrls,p.proowner=r.oid AS owner,to_regprocedure('support_actions.recent(uuid)') IS NOT NULL AS recovery FROM pg_roles r JOIN pg_proc p ON p.oid='support_actions.execute(text,integer,integer,text,uuid,timestamptz)'::regprocedure WHERE r.rolname=current_user")
 if(!rows.length||rows[0].rolsuper||rows[0].rolbypassrls||rows[0].owner||!rows[0].recovery)throw new Error('Use a dedicated non-owner support action login.')}catch(error){await pool.end();throw error}

 const call=async(claims,sql,args)=>{
  const client=await pool.connect()
  try{await client.query('BEGIN');await client.query("SELECT set_config('request.jwt.claims',$1,true)",[JSON.stringify({sub:claims.sub,tenant:claims.tenant,gregale_read_only:true,gregale_internal_notes:claims.gregale_internal_notes===true,gregale_support_actions:claims.gregale_support_actions})]);const result=await client.query(sql,args);await client.query('COMMIT');return result.rows[0]?.result}
  catch(error){await client.query('ROLLBACK').catch(()=>{});if(['40001','P0002'].includes(error.code))throw Object.assign(new Error('Case changed or confirmation expired.'),{status:409});if(error.code==='42501')throw Object.assign(new Error('Support action permission refused.'),{status:403});throw Object.assign(new Error('Action outcome is unresolved. Check Recent actions.'),{status:503})}finally{client.release()}
 }
 const args=request=>[request.action,request.caseId,request.version,request.body??null,request.id,new Date(request.reviewExpires).toISOString()]
 return {
  register:(claims,request)=>call(claims,"SELECT support_actions.register($1,$2,$3,$4,$5,$6)",args(request)),
  recent:(claims,id=null)=>call(claims,"SELECT support_actions.recent($1) AS result",[id]),
  async execute(claims,request){
   await call(claims,'SELECT support_actions.submitted($1)',[request.id])
   const result=await call(claims,'SELECT support_actions.execute($1,$2,$3,$4,$5,$6) AS result',args(request))
   if(result?.rejected)throw Object.assign(new Error('Case changed or confirmation expired. Preview again.'),{status:409})
   return result
  },
  close:()=>pool.end()
 }
}

export function actionResult(value,action){
 if(!value||value.action!==action||!Number.isInteger(value.version)||value.version<1||!['open','closed'].includes(value.status)||action==='add_reply'&&(!Number.isInteger(value.reply_id)||value.reply_id<1)||['assign_case','unassign_case'].includes(action)&&!(value.assignee===null||typeof value.assignee==='string'&&value.assignee.length<=256))throw Object.assign(new Error('Action outcome is uncertain. Retry the same confirmation.'),{status:503})
 if(action==='add_note'&&(!Number.isInteger(value.note_id)||value.note_id<1))throw Object.assign(new Error('Note outcome is unresolved. Check Recent actions.'),{status:503})
 if(action==='set_priority'&&(!['low','normal','high','urgent'].includes(value.priority)||typeof value.response_due_at!=='string'||!Number.isFinite(Date.parse(value.response_due_at))))throw Object.assign(new Error('Priority outcome is unresolved. Check Recent actions.'),{status:503})
 return {...(action==='add_note'?{note_id:value.note_id}:{}),action:value.action,version:value.version,status:value.status,...(action==='add_reply'?{reply_id:value.reply_id}:{}),...(['assign_case','unassign_case'].includes(action)?{assignee:value.assignee}:{}),...(action==='set_priority'?{priority:value.priority,response_due_at:value.response_due_at}:{})}
}
