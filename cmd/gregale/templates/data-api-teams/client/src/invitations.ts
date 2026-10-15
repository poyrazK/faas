import { dataRequest, validateRpcResponse } from '@gregale/data'
import type { Database } from './database.types.js'
import type { teamsClient } from './orders.js'
const fields = 'invitation_id,tenant_id,issuer,invited_role,expires_at,created_at,revoked_at,accepted_at,accepted_subject,accepted_revision' as const
export type Invitation = Pick<Database['api']['Tables']['team_invitations']['Row'], 'invitation_id' | 'tenant_id' | 'issuer' | 'invited_role' | 'expires_at' | 'created_at' | 'revoked_at' | 'accepted_at' | 'accepted_subject' | 'accepted_revision'>
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
const secretPattern = /^[0-9a-f]{64}$/
const date = (v: unknown): v is string => typeof v === 'string' && /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,6})?(?:Z|[+-]\d{2}:\d{2})$/.test(v) && Number.isFinite(Date.parse(v))
export function parseInvitation(value: unknown): Invitation {
  const row = value as Record<string, unknown>
  if (!row || typeof row !== 'object' || !uuid.test(String(row.invitation_id)) || typeof row.tenant_id !== 'string' || !row.tenant_id || typeof row.issuer !== 'string' || !row.issuer || !['member','admin'].includes(String(row.invited_role)) || !date(row.expires_at) || !date(row.created_at) || ![row.revoked_at,row.accepted_at].every(v=>v===null || date(v)) || !(row.accepted_at===null ? row.accepted_subject===null && row.accepted_revision===null : typeof row.accepted_subject==='string' && !!row.accepted_subject && uuid.test(String(row.accepted_revision))) || row.revoked_at!==null && row.accepted_at!==null) throw new Error('Invalid invitation response')
  return Object.fromEntries(fields.split(',').map(key=>[key,row[key]])) as Invitation
}
export function invitationLink(base: string, team: string, id: string, secret: string) {
  if (!uuid.test(id) || !secretPattern.test(secret) || !team || team.length>256) throw new Error('Invalid invitation')
  const url = new URL(base)
  if (!['http:','https:'].includes(url.protocol) || url.username || url.password) throw new Error('Invalid invitation URL')
  url.search = ''; url.hash = new URLSearchParams({ team, invitation: id, secret }).toString()
  return url.toString()
}
export function readInvitationLink(link: string) {
  const url = new URL(link), values = new URLSearchParams(url.hash.slice(1))
  const team=values.get('team') ?? '', id=(values.get('invitation') ?? '').toLowerCase(), secret=values.get('secret') ?? ''
  if (!['http:','https:'].includes(url.protocol) || url.username || url.password || !team || team.length>256 || !uuid.test(id) || !secretPattern.test(secret) || ['team','invitation','secret'].some(k=>values.getAll(k).length!==1) || [...values.keys()].some(k=>!['team','invitation','secret'].includes(k))) throw new Error('Invalid invitation link')
  return { team, id, secret }
}
export function teamInvitations(client: ReturnType<typeof teamsClient>) {
  return {
    async list(after?: string, size=20) {
      if (after && !uuid.test(after) || !Number.isInteger(size) || size<1 || size>100) throw new Error('Invalid invitation page')
      if ((await client.membership())?.role!=='owner') throw new Error('Owner required')
      let query=client.db.from('team_invitations').select(fields).eq('tenant_id',client.tenant).order('invitation_id').limit(size+1)
      if(after) query=query.gt('invitation_id',after)
      const checked=validateRpcResponse(await dataRequest(query,{timeoutMs:5000}),{parse(v){if(!Array.isArray(v))throw new Error();return v.map(parseInvitation)}})
      if(checked.outcome!=='validated' || checked.result.data.some(r=>r.tenant_id!==client.tenant))throw new Error('Could not load invitations')
      const rows=checked.result.data.slice(0,size)
      return {rows,next:checked.result.data.length>size?rows.at(-1)!.invitation_id:null}
    },
    async history() {
      if ((await client.membership())?.role!=='owner') throw new Error('Owner required')
      const result=await dataRequest(client.db.from('team_invitation_events').select('event_id,tenant_id,invitation_id,actor,action,occurred_at').eq('tenant_id',client.tenant).order('occurred_at',{ascending:false}).limit(20),{timeoutMs:5000})
      if(result.error || !Array.isArray(result.data) || result.data.some(r=>r.tenant_id!==client.tenant || !['create','accept','revoke'].includes(r.action) || typeof r.actor!=='string' || !uuid.test(r.event_id) || !uuid.test(r.invitation_id) || !date(r.occurred_at)))throw new Error('Could not load invitation history')
      return result.data
    },
  }
}
type Intent = Readonly<{kind:'create';id:string;secret:string;role:'member'|'admin';expires:string;actor:string} | {kind:'accept';id:string;secret:string;actor:string} | {kind:'revoke';id:string;actor:string}>
export type InvitationResult = {outcome:'confirmed';invitation:Invitation;secret?:string} | {outcome:'failed';message:string;uncertain:boolean} | {outcome:'busy'|'pending'|'empty'}
/** One immutable operation, explicit replay only. Secrets live in action memory;
 * reload loses recovery state, so keep the page open until confirmed. */
export function createInvitationForm(client: ReturnType<typeof teamsClient>) {
  let pending:Intent|null=null,busy=false
  async function verifiedActor() {
    const result=await dataRequest(client.db.rpc('team_invitation_identity',{}),{timeoutMs:5000})
    if(result.error || !Array.isArray(result.data) || result.data.length!==1 || typeof result.data[0].subject!=='string' || !result.data[0].subject || result.data[0].subject.length>256)throw new Error('Could not verify invitation actor')
    return result.data[0].subject
  }
  async function execute(input:unknown,recovering=false):Promise<InvitationResult> {
    if(busy)return {outcome:'busy'}
    if(!recovering && pending)return {outcome:'pending'}
    if(recovering && !pending)return {outcome:'empty'}
    busy=true
    try {
      if(!recovering) {
        const v=input as Record<string,unknown>
        if(!v || !['create','accept','revoke'].includes(String(v.kind)))throw new Error('invalid')
        if(v.kind==='accept') {
          if(typeof v.link!=='string')throw new Error('invalid')
          const link=readInvitationLink(v.link)
          if(link.team!==client.tenant)throw new Error('invalid')
          pending=Object.freeze({kind:'accept',id:link.id,secret:link.secret,actor:await verifiedActor()})
        } else {
          // Validate before retrieving any session or sending a request.
          if(v.kind==='create' && !['member','admin'].includes(String(v.role)) || v.kind==='revoke' && !uuid.test(String(v.id)))throw new Error('invalid')
          const member=await client.membership()
          if(member?.role!=='owner')return {outcome:'failed',message:'Only current owners can manage invitations.',uncertain:false}
          pending=v.kind==='revoke'?Object.freeze({kind:'revoke',id:(v.id as string).toLowerCase(),actor:member.subject}):Object.freeze({kind:'create',id:crypto.randomUUID(),secret:Array.from(crypto.getRandomValues(new Uint8Array(32)),b=>b.toString(16).padStart(2,'0')).join(''),role:v.role as 'member'|'admin',expires:new Date(Date.now()+24*60*60*1000).toISOString(),actor:member.subject})
        }
      }
      const operation=pending!
      if(recovering && operation.kind==='accept' && await verifiedActor()!==operation.actor)return {outcome:'failed',message:'Use the original signed-in user to resolve this invitation.',uncertain:true}
      if(recovering && operation.kind!=='accept') {
        const member=await client.membership()
        if(member?.subject!==operation.actor || member.role!=='owner')return {outcome:'failed',message:'Use the original owner to resolve this invitation operation.',uncertain:true}
      }
      const args={operation_id:operation.id,team_id:client.tenant}
      const query=operation.kind==='create'?client.db.rpc('create_team_invitation_once',{...args,invite_secret:operation.secret,invite_role:operation.role,valid_until:operation.expires}):operation.kind==='accept'?client.db.rpc('accept_team_invitation_once',{...args,invite_secret:operation.secret}):client.db.rpc('revoke_team_invitation_once',args)
      const result=await dataRequest(query.select(fields),{timeoutMs:5000})
      const checked=validateRpcResponse(result,{parse(v){
        if(!Array.isArray(v)||v.length!==1)throw new Error()
        const row=parseInvitation(v[0])
        if(row.invitation_id!==operation.id || row.tenant_id!==client.tenant || operation.kind==='create' && (row.issuer!==operation.actor || row.invited_role!==operation.role || Date.parse(row.expires_at)!==Date.parse(operation.expires)) || operation.kind==='accept' && (!row.accepted_at || row.accepted_subject!==operation.actor) || operation.kind==='revoke' && !row.revoked_at)throw new Error()
        return row
      }})
      if(checked.outcome==='validated'){pending=null;return {outcome:'confirmed',invitation:checked.result.data,...(operation.kind==='create'?{secret:operation.secret}:{})}}
      const messages:Record<string,string>={invitation_unavailable:'This invitation is unavailable, expired, revoked or used by another user.',already_team_member:'You already belong to this team. Your role has not changed.',invitation_already_accepted:'This invitation was accepted. Manage the member to change their access.',invitation_payload_conflict:'This key belongs to another invitation operation.',invalid_invitation:'Choose a valid invitation and member or admin role.',invalid_invitation_expiry:'The invitation expiry is invalid. Create a new invitation.'}
      const known=['PT400','PT409'].includes(result.error?.code??'')?messages[result.error!.message]:undefined
      if(!recovering && (known || [401,403].includes(result.status)))pending=null
      return {outcome:'failed',message:known??([401,403].includes(result.status)?'Check your signed-in session and current access.':'The operation could not be confirmed. Resolve it before starting another invitation operation.'),uncertain:pending!==null}
    }catch{return {outcome:'failed',message:pending?'The operation could not be confirmed. Keep this page open and resolve the original operation.':'Choose a valid link for this team or a member or admin role.',uncertain:pending!==null}}
    finally{busy=false}
  }
  return {get pending(){return Boolean(pending)},submit:(input:unknown)=>execute(input),reconcile:()=>execute(undefined,true)}
}
