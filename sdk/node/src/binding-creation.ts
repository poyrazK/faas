import { statusReadRetryable, statusReadRetryDelay } from './binding-status-retry.js';
import { usableBindingCreation } from './binding-runner.js';
import type { BindingVerificationProgress } from './binding-runner.js';
import { AppsService } from './generated/services/AppsService.js';
import { ProjectsService } from './generated/services/ProjectsService.js';
import type { BindingVerificationReview } from './generated/models/BindingVerificationReview.js';
import type { ProjectBindingVerificationReview } from './generated/models/ProjectBindingVerificationReview.js';
import type { BindingVerificationRun } from './generated/models/BindingVerificationRun.js';
import type { ProjectBindingVerificationRun } from './generated/models/ProjectBindingVerificationRun.js';
import type { CancelablePromise } from './generated/core/CancelablePromise.js';

export interface BindingVerificationCreationOptions {
 statusReadRetries?: number; onProgress?: (event:BindingVerificationProgress)=>void|Promise<void>;
 runId: string; ttlSeconds: number; maxConcurrency?: number;
 objectStorageWrite?: boolean; queuePublish?: boolean; signal?: AbortSignal;
}
export class BindingVerificationCreationError extends Error {
 constructor(public readonly runId: string, cause: unknown, public readonly attempted: boolean) {
  super(`verification creation ${runId} failed; inspect this run ID: ${cause instanceof Error ? cause.message : String(cause)}`, {cause});
  this.name='BindingVerificationCreationError';
 }
}
const equal=(a: unknown,b: unknown): boolean => canonical(a)===canonical(b);
const canonical=(v: unknown): string => JSON.stringify(v,(_k,x)=>x&&typeof x==='object'&&!Array.isArray(x)?Object.fromEntries(Object.entries(x).sort(([a],[b])=>a.localeCompare(b))):x);
const uuid=(s: string): boolean=> /^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/.test(s)&&s!=='00000000-0000-0000-0000-000000000000';
const dep=(s: string): boolean=>uuid(s)||/^[a-f0-9]{32}$/.test(s)&&!/^0+$/.test(s);
const deployment=(s: string): string=>s.replaceAll('-','');
const digest=(s: string): boolean=>/^[a-fA-F0-9]{64}$/.test(s);
function options(o: BindingVerificationCreationOptions): void {
 if(!Number.isInteger(o.statusReadRetries??3)||(o.statusReadRetries??3)<0||(o.statusReadRetries??3)>8)throw new Error('status read retries must be between zero and eight');
 if(o.objectStorageWrite!==undefined&&typeof o.objectStorageWrite!=='boolean'||o.queuePublish!==undefined&&typeof o.queuePublish!=='boolean'||!uuid(o.runId)||!Number.isInteger(o.ttlSeconds)||o.ttlSeconds<1||o.ttlSeconds>600||!Number.isInteger(o.maxConcurrency??2)||(o.maxConcurrency??2)<1||(o.maxConcurrency??2)>8) throw new Error('invalid run UUID, lifetime or concurrency');
}
function review(r: BindingVerificationReview,app: string,o: BindingVerificationCreationOptions): void {
 const at=Date.parse(r.reviewed_at);
 if(r.app!==app||!app||!r.scope||!dep(r.deployment_id)||!r.ready||r.blockers.length||!digest(r.review_digest)||!Number.isFinite(at)||at>Date.now()||r.plan.length>128||r.object_storage_write_required&&!o.objectStorageWrite||r.queue_publish_required&&!o.queuePublish||r.policy.scope!==r.scope||r.inventory.verification_deployment_id!==r.deployment_id||!equal(r.inventory.service_targets??{},r.service_targets)) throw new Error('review selection, readiness or explicit consent was not confirmed');
}
function graphReview(r: ProjectBindingVerificationReview,p: string,env: string,o: BindingVerificationCreationOptions): void {
 const names=Object.keys(r.members),at=Date.parse(r.reviewed_at);
 if(r.project!==p||r.environment!==env||!p||!env||r.manifest.project!==p||r.manifest.environment!==env||!r.ready||r.blockers.length||!digest(r.review_digest)||!Number.isFinite(at)||at>Date.now()||r.object_storage_write!==!!o.objectStorageWrite||r.queue_publish!==!!o.queuePublish||r.object_storage_write_required&&!o.objectStorageWrite||r.queue_publish_required&&!o.queuePublish||names.length<1||names.length>100||!equal(names.sort(),Object.keys(r.manifest.deployments).sort())||!equal(names.sort(),Object.keys(r.review_digests).sort())) throw new Error('graph review selection, readiness or explicit consent was not confirmed');
 for(const name of names) {const m=r.members[name]!; review(m,name,o); if(m.scope!==env||deployment(m.deployment_id)!==deployment(r.manifest.deployments[name]??'')||m.review_digest!==r.review_digests[name]||m.policy.revision!==r.manifest.expected_policy_revisions[name]||!equal(m.service_targets,r.manifest.service_targets[name]??{})) throw new Error('graph member selection was not confirmed');}
}
function receipt(r: BindingVerificationRun,v: BindingVerificationReview,o: BindingVerificationCreationOptions): boolean {
 const at=Date.parse(r.created_at),until=Date.parse(r.expires_at);
 return r.id===o.runId&&r.app===v.app&&deployment(r.deployment_id)===deployment(v.deployment_id)&&r.scope===v.scope&&r.review_digest===v.review_digest&&r.object_storage_write===!!o.objectStorageWrite&&r.queue_publish===!!o.queuePublish&&(r.max_concurrency||2)===(o.maxConcurrency||2)&&r.policy.revision===v.policy.revision&&equal(r.inventory.service_targets??{},v.service_targets)&&Number.isFinite(at)&&at<=Date.now()&&until-at===o.ttlSeconds*1000&&equal(r.steps.map(s=>s.plan),v.plan);
}
function graphReceipt(r: ProjectBindingVerificationRun,v: ProjectBindingVerificationReview,o: BindingVerificationCreationOptions): boolean {
 if(r.id!==o.runId||r.project!==v.project||r.environment!==v.environment||r.review_digest!==v.review_digest||!equal(r.manifest,v.manifest)||r.object_storage_write!==!!o.objectStorageWrite||r.queue_publish!==!!o.queuePublish||(r.max_concurrency||2)!==(o.maxConcurrency||2)||!equal(Object.keys(r.members).sort(),Object.keys(v.members).sort())) return false;
 return Object.entries(r.members).every(([name,m])=>uuid(m.id)&&receipt(m,v.members[name]!,{...o,runId:m.id})&&m.graph_run_id===r.id&&Date.parse(m.created_at)===Date.parse(r.created_at)&&Date.parse(m.expires_at)===Date.parse(r.expires_at));
}
async function request<T>(start: ()=>CancelablePromise<T>,signal?: AbortSignal): Promise<T> {
 signal?.throwIfAborted(); const pending=start(); const abort=():void=>pending.cancel(); signal?.addEventListener('abort',abort,{once:true});
 try {const value=await pending; signal?.throwIfAborted(); return value;} finally {signal?.removeEventListener('abort',abort);}
}
async function bounded<T>(operation:()=>Promise<T>,signal:AbortSignal):Promise<T>{
 signal.throwIfAborted();let abort!:()=>void;
 const stopped=new Promise<never>((_resolve,reject)=>{abort=()=>reject(signal.reason);signal.addEventListener('abort',abort,{once:true});});
 try{return await Promise.race([operation(),stopped]);}finally{signal.removeEventListener('abort',abort);}
}
// Mirrors BindingVerificationCreationReconcileTimeoutMilliseconds in pkg/api/limits.go.
const creationRecoveryMilliseconds = 5000;
async function creationDelay(delay:number,signal:AbortSignal):Promise<void>{
 signal.throwIfAborted();
 await new Promise<void>((resolve,reject)=>{
  const timer=setTimeout(()=>{signal.removeEventListener('abort',stop);resolve();},delay);
  const stop=():void=>{clearTimeout(timer);signal.removeEventListener('abort',stop);reject(signal.reason);};
  signal.addEventListener('abort',stop,{once:true});if(signal.aborted)stop();
 });
}
async function recoverCreation<T extends BindingVerificationRun|ProjectBindingVerificationRun>(cause:unknown,began:number,o:BindingVerificationCreationOptions,read:()=>CancelablePromise<T>,check:(r:T)=>void):Promise<T>{
 const controller=new AbortController(),until=Math.min(began+o.ttlSeconds*1000,Date.now()+creationRecoveryMilliseconds),signal=o.signal?AbortSignal.any([o.signal,controller.signal]):controller.signal;
 const timer=setTimeout(()=>controller.abort(new Error('creation reconciliation deadline expired')),Math.max(0,until-Date.now()));
 const emit=async(event:BindingVerificationProgress):Promise<void>=>{await bounded(async()=>{await o.onProgress?.(Object.freeze(event));},signal);};
 try{
  signal.throwIfAborted();if(Date.now()>=until)throw new Error('creation reconciliation deadline expired');
  await emit({run_id:o.runId,step:-1,status:'reconciling',operation:'creation'});
  for(let attempt=0;;attempt++){
   signal.throwIfAborted();if(Date.now()>=until)throw new Error('creation reconciliation deadline expired');
   let result:T;
   try{result=await request(read,signal);}catch(error){
    signal.throwIfAborted();if(attempt>=(o.statusReadRetries??3)||!statusReadRetryable(error))throw error;
    const delay=Math.max(0,Math.min(statusReadRetryDelay(attempt+1),until-Date.now()));
    await emit({run_id:o.runId,step:-1,status:'retrying',retry_attempt:attempt+1,retry_delay_ms:delay});
    await creationDelay(delay,signal);continue;
   }
   check(result);usableBindingCreation(result);signal.throwIfAborted();if(Date.now()>=until)throw new Error('creation reconciliation deadline expired');return result;
  }
 }catch(error){throw new AggregateError([cause,error],'could not reconcile original receipt creation',{cause});}
 finally{clearTimeout(timer);}
}
async function appOperation(app: string,input: BindingVerificationReview,inputOptions: BindingVerificationCreationOptions,read: boolean): Promise<BindingVerificationRun> {
 const v=structuredClone(input),o={...inputOptions}; let attempted=false;const began=Date.now();
 try {options(o);review(v,app,o);o.signal?.throwIfAborted();
 let r:BindingVerificationRun;
 try{r=await request(()=>{attempted=!read; return read?AppsService.getBindingVerificationRun({slug:app,run:o.runId}):AppsService.createBindingVerificationRun({slug:app,requestBody:{id:o.runId,deployment_id:v.deployment_id,service_targets:v.service_targets,review_digest:v.review_digest,ttl_seconds:o.ttlSeconds,max_concurrency:o.maxConcurrency??2,object_storage_write:!!o.objectStorageWrite,queue_publish:!!o.queuePublish}});},o.signal);}catch(error){
  if(read||o.signal?.aborted||!statusReadRetryable(error))throw error;
  r=await recoverCreation(error,began,o,()=>AppsService.getBindingVerificationRun({slug:app,run:o.runId}),r=>{if(!receipt(r,v,o)||r.graph_run_id)throw new Error('server returned another receipt or reviewed selection');});
 }
 if(!receipt(r,v,o)||r.graph_run_id) throw new Error('server returned another receipt or reviewed selection'); return r;
 } catch(cause) {throw new BindingVerificationCreationError(o.runId,cause,attempted);}
}
async function graphOperation(project: string,environment: string,input: ProjectBindingVerificationReview,inputOptions: BindingVerificationCreationOptions,read: boolean): Promise<ProjectBindingVerificationRun> {
 const v=structuredClone(input),o={...inputOptions}; let attempted=false;const began=Date.now();
 try {options(o);graphReview(v,project,environment,o);o.signal?.throwIfAborted();
 let r:ProjectBindingVerificationRun;
 try{r=await request(()=>{attempted=!read; return read?ProjectsService.getProjectBindingVerificationRun({slug:project,environment,run:o.runId}):ProjectsService.createProjectBindingVerificationRun({slug:project,environment,requestBody:{id:o.runId,manifest:v.manifest,review_digests:v.review_digests,review_digest:v.review_digest,ttl_seconds:o.ttlSeconds,max_concurrency:o.maxConcurrency??2,object_storage_write:!!o.objectStorageWrite,queue_publish:!!o.queuePublish}});},o.signal);}catch(error){
  if(read||o.signal?.aborted||!statusReadRetryable(error))throw error;
  r=await recoverCreation(error,began,o,()=>ProjectsService.getProjectBindingVerificationRun({slug:project,environment,run:o.runId}),r=>{if(!graphReceipt(r,v,o))throw new Error('server returned another graph receipt or reviewed selection');});
 }
 if(!graphReceipt(r,v,o)) throw new Error('server returned another graph receipt or reviewed selection'); return r;
 } catch(cause) {throw new BindingVerificationCreationError(o.runId,cause,attempted);}
}
export const createBindingVerificationFromReview=(app: string,review: BindingVerificationReview,options: BindingVerificationCreationOptions): Promise<BindingVerificationRun>=>appOperation(app,review,options,false);
export const reconcileBindingVerificationCreation=(app: string,review: BindingVerificationReview,options: BindingVerificationCreationOptions): Promise<BindingVerificationRun>=>appOperation(app,review,options,true);
export const createProjectBindingVerificationFromReview=(project: string,environment: string,review: ProjectBindingVerificationReview,options: BindingVerificationCreationOptions): Promise<ProjectBindingVerificationRun>=>graphOperation(project,environment,review,options,false);
export const reconcileProjectBindingVerificationCreation=(project: string,environment: string,review: ProjectBindingVerificationReview,options: BindingVerificationCreationOptions): Promise<ProjectBindingVerificationRun>=>graphOperation(project,environment,review,options,true);
