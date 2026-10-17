import type { BindingVerificationRun } from './generated/models/BindingVerificationRun.js';
import type { ProjectBindingVerificationRun } from './generated/models/ProjectBindingVerificationRun.js';
import type { BindingVerificationQualification } from './generated/models/BindingVerificationQualification.js';
type Run=BindingVerificationRun|ProjectBindingVerificationRun;
export function pendingQualification(run: Run): BindingVerificationQualification {
 const q=run.qualification,at=Date.parse(q?.checked_at??'');
 if(run.status!=='pending'||!q||!Number.isFinite(at)||at>Date.now()||at>=Date.parse(run.expires_at)||q.conditions.length<1||q.conditions.length>12800)throw new Error('server did not confirm pending qualification');
 const members='members' in run?Object.values(run.members):[run];
 for(const c of q.conditions){
  const member=members.find(m=>m.app===c.app);if(!member||at<Date.parse(member.created_at))throw new Error('qualification changed the receipt selection');
  if(['runtime_updating','runtime_stale'].includes(c.code)){if(c.type||c.name||c.binding||c.scope!==member.scope)throw new Error('invalid runtime qualification condition');continue;}
  const allowed=['queue_consumer_unobserved','queue_consumer_stale'].includes(c.code)?c.type==='queue':['application_adoption_unknown','application_ack_stale','application_ack_candidate_unobserved'].includes(c.code)&&['postgres','object_storage'].includes(c.type??'');
  const candidates=[...member.steps.map(s=>s.plan),...member.inventory.bindings];
  if(!allowed||!candidates.some(p=>p.type===c.type&&p.name===c.name&&p.binding===c.binding&&p.scope===c.scope))throw new Error('invalid or unselected qualification condition');
 }
 return {checked_at:q.checked_at,conditions:q.conditions.map(c=>({app:c.app,code:c.code,...(c.type!==undefined?{type:c.type}:{}),...(c.name!==undefined?{name:c.name}:{}),...(c.binding!==undefined?{binding:c.binding}:{}),...(c.scope!==undefined?{scope:c.scope}:{})}))};
}
