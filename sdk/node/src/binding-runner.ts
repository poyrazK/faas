import { pendingQualification } from './binding-qualification.js';
import type { BindingVerificationQualification } from './generated/models/BindingVerificationQualification.js';
import { statusReadRetryable, statusReadRetryDelay } from './binding-status-retry.js';
import { appDiagnostics, projectDiagnostics, type BindingVerificationDiagnostics } from './binding-runner-diagnostics.js';
import { AppsService } from './generated/services/AppsService.js';
import { ProjectsService } from './generated/services/ProjectsService.js';
import type { BindingVerificationRun } from './generated/models/BindingVerificationRun.js';
import type { BindingVerificationRunStep } from './generated/models/BindingVerificationRunStep.js';
import type { ProjectBindingVerificationRun } from './generated/models/ProjectBindingVerificationRun.js';
import type { CancelablePromise } from './generated/core/CancelablePromise.js';

export interface BindingVerificationProgress {
 run_id: string; operation?: 'admission'|'completion'|'creation'; qualification?: BindingVerificationQualification; retry_attempt?: number; retry_delay_ms?: number; app?: string; member_run_id?: string; step: number;
 resource_id?: string; resource_kind?: string; status: string;
}
export interface BindingVerificationRunnerOptions {
 statusReadRetries?: number;
 skipDiagnostics?: boolean;
 pollIntervalMs?: number; concurrency?: number; signal?: AbortSignal;
 /** Called serially on changes. Throwing stops observation and preserves work. */
 onProgress?: (progress: Readonly<BindingVerificationProgress>) => void | Promise<void>;
}
type Run = BindingVerificationRun | ProjectBindingVerificationRun;
export class BindingVerificationRecoveryError extends Error {
 constructor(public readonly runId: string, cause: unknown, public readonly run?: Run, public readonly diagnostics?: BindingVerificationDiagnostics) {
  super(`verification run ${runId} stopped; resume this run: ${cause instanceof Error ? cause.message : String(cause)}`, {cause});
  this.name = 'BindingVerificationRecoveryError';
 }
}
const uuid = (id: string): boolean => /^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/.test(id) && id !== '00000000-0000-0000-0000-000000000000';
const deploymentId = (id: string): string => id.length===32 ? `${id.slice(0,8)}-${id.slice(8,12)}-${id.slice(12,16)}-${id.slice(16,20)}-${id.slice(20)}` : id;
const members = (run: Run): BindingVerificationRun[] => 'members' in run ? Object.keys(run.members).sort().map(name => run.members[name]!) : [run];
const canonical = (value: unknown): string => JSON.stringify(value, (_key, item) => item && typeof item === 'object' && !Array.isArray(item) ? Object.fromEntries(Object.entries(item).sort(([a],[b]) => a.localeCompare(b))) : item);
function immutable(run: Run): string {
 const frozen = structuredClone(run);
 const clean = (member: BindingVerificationRun): void => {
  member.status = 'pending'; delete member.reason; delete member.qualification;
  for(const step of member.steps) { step.status=''; delete step.resource_id; delete step.resource_kind; step.owned=false; }
 };
 if('members' in frozen) { frozen.status='pending'; delete frozen.reason; delete frozen.qualification; for(const member of members(frozen)) clean(member); }
 else clean(frozen);
 return canonical(frozen);
}
function validate(run: Run): void {
 if(!uuid(run.id) || !['pending','passed'].includes(run.status)) throw new Error(`run is not resumable: ${run.status}`);
 for(const member of members(run)) {
  const created = Date.parse(member.created_at), expires = Date.parse(member.expires_at);
  if((member.max_concurrency??0)<0 || (member.max_concurrency??0)>8 || !uuid(member.id) || !member.app || !uuid(deploymentId(member.deployment_id)) || !member.review_digest || !Number.isFinite(created) || !Number.isFinite(expires) || created>Date.now() || expires<=Date.now() || expires<=created || expires-created>600_000 || member.steps.length>128 || !['pending','passed'].includes(member.status)) throw new Error('server did not confirm a resumable member and original deadline');
 }
 if('members' in run) {
  const names=Object.keys(run.members);
  if(!run.review_digest || names.length<1 || names.length>100 || names.length!==Object.keys(run.manifest.deployments).length || run.manifest.project!==run.project || run.manifest.environment!==run.environment) throw new Error('server did not confirm graph membership');
  for(const name of names) {
   const r=run.members[name]!;
   if((r.max_concurrency||2)!==(run.max_concurrency||2) || r.app!==name || r.graph_run_id!==run.id || deploymentId(r.deployment_id)!==deploymentId(run.manifest.deployments[name]??'') || r.scope!==run.environment || r.created_at!==run.created_at || r.expires_at!==run.expires_at || r.policy.revision!==run.manifest.expected_policy_revisions[name] || canonical(r.inventory.service_targets??{})!==canonical(run.manifest.service_targets[name]??{}) || r.queue_publish!==run.queue_publish || r.object_storage_write!==run.object_storage_write) throw new Error('server changed the graph member selection');
  }
 }
}
type Candidate = {member: BindingVerificationRun; index: number; step: BindingVerificationRunStep};
function inspect(run: Run, known: Map<string,string>): {pending: Candidate[]; active: number} {
 const pending: Candidate[]=[];let active=0;
 for(const member of members(run)) for(const [index,step] of member.steps.entries()) {
  const key=`${member.app}/${index}`, ref=canonical([step.resource_id,step.resource_kind,step.owned]);
  if(known.has(key)&&known.get(key)!==ref) throw new Error('server changed an admitted resource identity or owner');
  if(step.resource_id) {
   if(!uuid(step.resource_id)||step.resource_kind!==(step.plan.type==='queue'?'queue':'task')) throw new Error('server returned an invalid resource identity');
   const admits=['probe','queue-canary'].includes(step.plan.action);
   if(step.owned!==admits) throw new Error('server did not confirm the reviewed resource owner');
   known.set(key,ref);
   if(step.status==='succeeded') continue;
   if((step.resource_kind==='task'&&['queued','restoring','running'].includes(step.status)) || (step.resource_kind==='queue'&&['published','dispatching'].includes(step.status))) {active++;continue;}
   throw new Error(`resource ${step.resource_id} did not succeed: ${step.status}`);
  }
  if(step.owned||step.resource_kind) throw new Error('incomplete resource identity');
  if(['reuse','observe','skip'].includes(step.plan.action)) continue;
  if(['probe','queue-canary'].includes(step.plan.action)&&member.status!=='passed') pending.push({member,index,step});
  else throw new Error(`reviewed step cannot be executed: ${step.plan.action}`);
 }
 return {pending,active};
}
// Internal creation recovery shares the runner's resumability/resource checks.
export function usableBindingCreation(run:Run):void {
 validate(run);const state=inspect(run,new Map<string,string>());
 if(run.status==='passed'&&(state.pending.length||state.active||run.qualification||members(run).some(m=>m.status!=='passed'||m.qualification)))throw new Error('passed creation has unfinished resources');
 if('members'in run){const ids=[run.id,...members(run).map(m=>m.id)];if(new Set(ids).size!==ids.length)throw new Error('duplicate graph receipt identity');}
}
async function request<T>(operation: () => CancelablePromise<T>, signal: AbortSignal): Promise<T> {
 signal.throwIfAborted();const pending=operation();
 const abort=():void=>pending.cancel();signal.addEventListener('abort',abort,{once:true});
 try { const result=await pending;signal.throwIfAborted();return result; }
 catch(error) {signal.throwIfAborted();throw error;}
 finally {signal.removeEventListener('abort',abort);}
}
async function progress(callback: NonNullable<BindingVerificationRunnerOptions['onProgress']>,event: BindingVerificationProgress,signal: AbortSignal): Promise<void> {
 signal.throwIfAborted();
 const result=callback(Object.freeze(event));signal.throwIfAborted();
 let abort!:()=>void;
 const stopped=new Promise<never>((_resolve,reject)=>{abort=()=>reject(signal.reason);signal.addEventListener('abort',abort,{once:true});});
 try {await Promise.race([Promise.resolve(result),stopped]);}
 finally {signal.removeEventListener('abort',abort);}
}
async function wait(ms: number,signal: AbortSignal): Promise<void> {
 signal.throwIfAborted();
 await new Promise<void>((resolve,reject)=>{
  const abort=():void=>{clearTimeout(timer);signal.removeEventListener('abort',abort);reject(signal.reason);};
  const timer=setTimeout(()=>{signal.removeEventListener('abort',abort);resolve();},ms);
  signal.addEventListener('abort',abort,{once:true});
 });
}
async function drive<T extends Run>(id: string, options: BindingVerificationRunnerOptions, read: (resume: boolean,signal: AbortSignal)=>Promise<T>, complete:(signal:AbortSignal)=>Promise<T>, check:(run:T)=>void, diagnose:(signal:AbortSignal,original?:T)=>Promise<BindingVerificationDiagnostics>): Promise<T> {
 const retries=options.statusReadRetries??3;
 if(!Number.isInteger(retries)||retries<0||retries>8) throw new RangeError('status read retries must be between zero and eight');
 const interval=options.pollIntervalMs??1000, concurrency=options.concurrency??2;
 if(!Number.isFinite(interval)||interval<=0||!Number.isInteger(concurrency)||concurrency<1||concurrency>8) throw new RangeError('poll interval must be positive and concurrency must be between 1 and 8');
 const controller=new AbortController();let signal=options.signal ? AbortSignal.any([options.signal,controller.signal]) : controller.signal;
 let timer:ReturnType<typeof setTimeout>|undefined=setTimeout(()=>controller.abort(new Error('verification observation deadline expired')),600_000);let current:T|undefined,original:T|undefined;let localFailure=false;
 try {
  if(!uuid(id)) throw new Error('run ID must be a nonzero canonical UUID');
  current=await read(true,signal);check(current);validate(current);original=structuredClone(current);
  clearTimeout(timer);
  timer=setTimeout(()=>controller.abort(new Error('original verification run deadline expired')),Math.max(0,Date.parse(current.expires_at)-Date.now()));
  signal=options.signal ? AbortSignal.any([options.signal,controller.signal]) : controller.signal;
  let waiting:BindingVerificationQualification|undefined;
  let confirmed=false;
  const frozen=immutable(current),known=new Map<string,string>(),seen=new Map<string,string>();
  const notify=async(run:T):Promise<void>=>{
   const events:BindingVerificationProgress[]=[{run_id:id,step:-1,status:run.status==='passed'&&!confirmed?'qualifying':run.status==='pending'&&waiting?'waiting_for_qualification':run.status,...(waiting?{qualification:structuredClone(waiting)}:{})}];
   for(const member of members(run)) for(const [index,step] of member.steps.entries()) events.push({run_id:id,app:member.app,member_run_id:member.id,step:index,resource_id:step.resource_id,resource_kind:step.resource_kind,status:step.status});
   if(options.onProgress) for(const event of events) {const key=`${event.app??''}/${event.step}`,value=canonical(event);if(seen.get(key)!==value) {signal.throwIfAborted();try {await progress(options.onProgress,event,signal);} catch(error) {localFailure=true;throw error;}seen.set(key,value);}}
   signal.throwIfAborted();
   if(Date.parse(run.expires_at)<=Date.now())throw new Error('original verification run deadline expired');
  };
  const readFresh=async():Promise<T>=>{
   for(let attempt=0;;attempt++) {
    signal.throwIfAborted();
    if(Date.parse(original!.expires_at)<=Date.now())throw new Error('original verification run deadline expired');
    try {return await read(false,signal);}
    catch(error) {
     signal.throwIfAborted();
     if(attempt>=retries||!statusReadRetryable(error))throw error;
     const delay=Math.max(0,Math.min(statusReadRetryDelay(attempt+1),Date.parse(original!.expires_at)-Date.now()));
     const event:BindingVerificationProgress={run_id:id,step:-1,status:'retrying',retry_attempt:attempt+1,retry_delay_ms:delay};
     if(options.onProgress){try {await progress(options.onProgress,event,signal);}catch(cause){localFailure=true;throw cause;}seen.set('/-1',canonical(event));}
     await wait(delay,signal);
    }
   }
  };
  const reconcile=async(error:unknown,operation:'admission'|'completion',target?:{member:BindingVerificationRun;index:number;step:BindingVerificationRunStep}):Promise<T>=>{
   signal.throwIfAborted();
   if(!statusReadRetryable(error))throw error;
   try {
    const event:BindingVerificationProgress={run_id:id,step:target?.index??-1,status:'reconciling',operation,...(target?{app:target.member.app,member_run_id:target.member.id}:{})};
    if(options.onProgress){try{await progress(options.onProgress,event,signal);}catch(cause){localFailure=true;throw cause;}seen.set(`${event.app??''}/${event.step}`,canonical(event));}
    signal.throwIfAborted();
    const fresh=await readFresh();check(fresh);validate(fresh);
    if(immutable(fresh)!==frozen)throw new Error('reconciliation changed the original review or deadline');
    const state=inspect(fresh,known);
    if(target){
     const member=members(fresh).find(m=>m.app===target.member.app&&m.id===target.member.id),step=member?.steps[target.index];
     if(!step?.owned||!step.resource_id||!uuid(step.resource_id)||step.resource_kind!==(target.step.plan.type==='queue'?'queue':'task')||canonical(step.plan)!==canonical(target.step.plan))throw new Error('admission was not confirmed');
    }else if(fresh.status!=='passed'||fresh.qualification||state.pending.length||state.active||members(fresh).some(m=>m.status!=='passed'||m.qualification))throw new Error('completion was not confirmed');
    return fresh;
   }catch(cause){throw new AggregateError([error,cause],'could not reconcile the original verification operation',{cause:error});}
  };
  for(;;) {
   signal.throwIfAborted();check(current);validate(current);
   if(immutable(current)!==frozen) throw new Error('server changed the original review or deadline');
   const state=inspect(current,known);await notify(current);
   if(current.status==='passed') {
    if(state.pending.length||state.active||('members' in current&&members(current).some(member=>member.status!=='passed'))) throw new Error('server did not confirm a passed run with finished members');
    if(confirmed)return current;
   }
   if(!state.pending.length&&!state.active) {
    let done:T;
    try{done=await complete(signal);}catch(error){done=await reconcile(error,'completion');}
    check(done);validate(done);
    if(immutable(done)!==frozen) throw new Error('server changed the original review at completion');

    const completionState=inspect(done,known);
    if(done.status==='pending'){
     if(completionState.pending.length||completionState.active)throw new Error('pending qualification has unfinished work');
     waiting=pendingQualification(done);current=done;await notify(current);
     await wait(Math.max(0,Math.min(interval,Date.parse(original!.expires_at)-Date.now())),signal);
     current=await readFresh();continue;
    }
    if(done.status!=='passed'||done.qualification)throw new Error('server did not confirm a passed run for the original review');
    current=done;waiting=undefined;confirmed=true;continue;
   }
   let admitted=false,budgetFull=false,reconciled=false;
   for(const next of state.pending) {
    if(state.active>=concurrency) break;
    let step:BindingVerificationRunStep;
    try { step=await request(()=>AppsService.admitBindingVerificationRunStep({slug:next.member.app,run:next.member.id,step:next.index}),signal); }
    catch(error) {
     const problem=error && typeof error==='object' ? ('problem' in error ? error.problem : 'body' in error ? error.body : undefined) : undefined;
     if(error && typeof error==='object' && 'status' in error && error.status===429 && problem && typeof problem==='object' && 'code' in problem && problem.code==='binding_verification_budget_full') {budgetFull=true;break;}
     current=await reconcile(error,'admission',next);reconciled=true;break;
    }
    if(canonical(step.plan)!==canonical(next.step.plan)||!step.owned||step.resource_kind!==(step.plan.type==='queue'?'queue':'task')||!step.resource_id||!uuid(step.resource_id)) throw new Error('server did not confirm the reviewed admission');
    next.member.steps[next.index]=step;inspect(current,known);await notify(current);
    if(step.status!=='succeeded') state.active++;admitted=true;
   }
   if(reconciled)continue;
   if(!admitted||budgetFull) await wait(Math.max(0,Math.min(interval,Date.parse(current.expires_at)-Date.now())),signal);
   current=await readFresh();
  }
  } catch(error) {
  let diagnostics:BindingVerificationDiagnostics|undefined;
  if(!options.skipDiagnostics&&!localFailure&&!signal.aborted&&uuid(id)) {
   const diagnosticController=new AbortController();
   const deadline=setTimeout(()=>diagnosticController.abort(),2000);
   const diagnosticSignal=AbortSignal.any([signal,diagnosticController.signal]);
   try {diagnostics=await diagnose(diagnosticSignal,original);diagnosticSignal.throwIfAborted();}
   catch {diagnostics=undefined;}
   finally {clearTimeout(deadline);}
  }
  throw new BindingVerificationRecoveryError(id,error,current?structuredClone(current):undefined,diagnostics);
 }
 finally {if(timer!==undefined)clearTimeout(timer);}
}
/** Resume an existing receipt. Local aborts and transport failures preserve work. */
export function runBindingVerification(app: string,id: string,options:BindingVerificationRunnerOptions={}):Promise<BindingVerificationRun> {
 return drive(id,options,(resume,signal)=>request(()=>resume?AppsService.resumeBindingVerificationRun({slug:app,run:id}):AppsService.getBindingVerificationRun({slug:app,run:id}),signal),signal=>request(()=>AppsService.completeBindingVerificationRun({slug:app,run:id}),signal),run=>{if(run.id!==id||run.app!==app)throw new Error('server returned another app run');},async(signal,original)=>appDiagnostics(await request(()=>AppsService.getBindingVerificationRunDiagnostics({slug:app,run:id}),signal),app,id,original));
}
/** One resource budget covers the entire graph. Completion is server-authoritative. */
export function runProjectBindingVerification(project:string,environment:string,id:string,options:BindingVerificationRunnerOptions={}):Promise<ProjectBindingVerificationRun> {
 return drive(id,options,(resume,signal)=>request(()=>resume?ProjectsService.resumeProjectBindingVerificationRun({slug:project,environment,run:id}):ProjectsService.getProjectBindingVerificationRun({slug:project,environment,run:id}),signal),signal=>request(()=>ProjectsService.completeProjectBindingVerificationRun({slug:project,environment,run:id}),signal),run=>{if(run.id!==id||run.project!==project||run.environment!==environment)throw new Error('server returned another project run');},async(signal,original)=>projectDiagnostics(await request(()=>ProjectsService.getProjectBindingVerificationRunDiagnostics({slug:project,environment,run:id}),signal),project,environment,id,original));
}
