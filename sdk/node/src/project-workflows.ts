/** Reviewed, account-owned project plans and receipt-only apply recovery. */
import { constants, openAsBlob } from 'node:fs';
import { open, realpath, mkdtemp, rename, unlink, rm } from 'node:fs/promises';
import { resolve, dirname, basename, join } from 'node:path';
import { tmpdir } from 'node:os';
import { createHash, randomUUID } from 'node:crypto';
import type { ClientConfig } from './client-instance.js';
import { scopedRequest } from './request-scope.js';
import type { RequestOptions } from './request-options.js';
import { prepareSource, type SourcePreparationOptions } from './source-preparation.js';
import { bindDeploymentWaiters, snapshot, WaiterError, type DeploymentWaitOptions, type WaiterSnapshot } from './deployment-waiters.js';
import type { PlanResponse } from './generated/models/PlanResponse.js';
import type { ApplyResponse } from './generated/models/ApplyResponse.js';
import type { ProjectInitialSecrets } from './generated/models/ProjectInitialSecrets.js';
import type { ProjectSubmissionReceipt } from './generated/models/ProjectSubmissionReceipt.js';
import type { ReleaseTimeline } from './generated/models/ReleaseTimeline.js';

export type ProjectWorkflowCode = 'invalid_plan' | 'invalid_state' | 'state_mismatch' | 'busy' | 'state_failed' | 'preview_failed' | 'preview_expired' | 'plan_stale' | 'permission_denied' | 'blocked' | 'acceptance_unknown' | 'invalid_response' | 'poll_limit' | 'progress_failed' | 'timeout' | 'cancelled';
export type ProjectWorkflowPhase = 'preparing' | 'previewing' | 'applying' | 'recovering' | 'observing' | 'complete';
export interface ReviewedProjectPlan {
  readonly version: 1;
  readonly base_url: string;
  readonly project_slug: string;
  readonly environment: string | null;
  readonly source: { readonly size: number; readonly sha256: string };
  readonly preview: WaiterSnapshot<PlanResponse>;
  readonly fingerprint: string;
}
export interface ProjectWorkflowProgress {
  readonly phase: ProjectWorkflowPhase;
  readonly projectSlug: string;
  readonly attemptId: string | null;
  readonly elapsedMs: number;
  readonly workload?: string;
  readonly outcome?: ProjectWorkloadOutcome['outcome'];
}
export interface ProjectWorkflowOptions extends Omit<RequestOptions, 'timeoutMs'> {
  readonly timeoutMs: number;
  readonly onProgress?: (event: ProjectWorkflowProgress) => void | Promise<void>;
}
export interface ProjectPreviewInput {
  readonly directory: string;
  readonly projectSlug: string;
  readonly environment?: string;
  readonly only?: readonly string[];
  readonly noTriggers?: boolean;
  readonly appSlugs?: Readonly<Record<string,string>>;
}
export interface ProjectPreviewOptions extends ProjectWorkflowOptions {
  readonly preparation?: Omit<SourcePreparationOptions, 'sourceRoot' | 'timeoutMs' | 'signal' | 'onProgress'>;
}
export interface ProjectApplyOptions extends ProjectWorkflowOptions {
  readonly statePath: string;
  /** Transient credentials/configuration; never saved to the journal. */
  readonly approvalToken?: string;
  readonly initialSecrets?: ProjectInitialSecrets;
  readonly wait?: Pick<DeploymentWaitOptions, 'minDelayMs' | 'maxDelayMs' | 'maxPolls' | 'stopOnBlocked'>;
  readonly maxReceiptPolls?: number;
}
export interface ProjectApplicationState {
  readonly version: 1;
  readonly base_url: string;
  readonly project_slug: string;
  readonly environment: string | null;
  readonly plan_fingerprint: string;
  readonly attempt_id: string;
  readonly stage: 'prepared' | 'applying' | 'accepted' | 'rejected' | 'complete';
  readonly rejection: ProjectWorkflowCode | null;
}
export interface ProjectWorkloadOutcome {
  readonly slug: string;
  readonly appId: string | null;
  readonly deploymentId: string | null;
  readonly buildId: string | null;
  readonly outcome: 'succeeded' | 'failed' | 'blocked' | 'unchanged' | 'unknown';
  readonly code?: string;
  readonly timeline?: WaiterSnapshot<ReleaseTimeline>;
}
export interface ProjectApplicationResult {
  readonly outcome: 'succeeded' | 'partial' | 'failed' | 'blocked';
  readonly attemptId: string;
  readonly statePath: string;
  readonly receipt: WaiterSnapshot<ProjectSubmissionReceipt>;
  readonly workloads: readonly ProjectWorkloadOutcome[];
}
export class ProjectWorkflowError extends Error {
  constructor(readonly code: ProjectWorkflowCode, readonly phase: ProjectWorkflowPhase,
    readonly state?: ProjectApplicationState, override readonly cause?: unknown) {
    super(`SDK project workflow stopped: ${code}`); this.name = 'ProjectWorkflowError';
  }
}
export interface ProjectWorkflows {
  previewLocal(input: ProjectPreviewInput, options: ProjectPreviewOptions): Promise<ReviewedProjectPlan>;
  applyPlan(plan: ReviewedProjectPlan, options: ProjectApplyOptions): Promise<ProjectApplicationResult>;
  resumePlanApplication(plan: ReviewedProjectPlan, options: ProjectApplyOptions): Promise<ProjectApplicationResult>;
}
const active = new Set<string>();
const slug = (s: unknown): s is string => typeof s === 'string' && /^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/.test(s);
const hash = (s: unknown): s is string => typeof s === 'string' && /^[a-f0-9]{64}$/.test(s);
function stable(value: unknown): string {
  if (typeof value === 'number') {
    if (!Number.isFinite(value)) throw new TypeError('Non-finite plan number');
    const bytes=Buffer.alloc(8);bytes.writeDoubleBE(value===0?0:value);return 'n'+bytes.toString('hex');
  }
  if (Array.isArray(value)) return '[' + value.map(stable).join(',') + ']';
  if (value && typeof value === 'object') return '{' + Object.keys(value).sort((a,b)=>Buffer.compare(Buffer.from(a),Buffer.from(b))).map(k => JSON.stringify(k)+':'+stable((value as Record<string,unknown>)[k])).join(',') + '}';
  return JSON.stringify(value);
}
function fingerprint(plan: ReviewedProjectPlan): string {
  const { fingerprint: _, ...rest } = plan;
  return createHash('sha256').update(stable(rest)).digest('hex');
}
function base(value: string): string {
  const url = new URL(value);
  if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.search || url.hash) throw new TypeError('Invalid project endpoint');
  return url.href.replace(/\/+$/, '');
}
const arrayFields=['workloads','managed','crons','apps','builds','unchanged','will_deploy','unaffected','skipped','removed','feature_hints','warnings','detection_warnings','async_routes','persisted_exclusions','stale_persisted_exclusions','can_apply_reasons'];
function normalize<T extends object>(value:T):T {const copy={...value} as Record<string,unknown>;for(const key of arrayFields)if(copy[key]===null)copy[key]=[];return copy as T;}
function validPreview(p: PlanResponse, target: string): boolean {
  return !!p && p.project_slug === target && typeof p.can_apply === 'boolean' && typeof p.plan_token === 'string' && !!p.plan_token &&
    ['workloads','managed','crons'].every(k => Array.isArray((p as unknown as Record<string,unknown>)[k])) &&
    [p.observed_apps,p.observed_crons,p.limit_apps,p.limit_crons].every(n=>Number.isSafeInteger(n)&&n>=0) &&
    p.workloads.every(w=>w&&typeof w.name==='string'&&!!w.name);
}
export function parseReviewedProjectPlan(value: unknown): ReviewedProjectPlan {
  try {
    const p = value as ReviewedProjectPlan;
    if (!p || Object.keys(p).sort().join() !== 'base_url,environment,fingerprint,preview,project_slug,source,version' || p.version !== 1 || base(p.base_url) !== p.base_url || !slug(p.project_slug) ||
      p.environment !== null && !slug(p.environment) || !p.source || Object.keys(p.source).sort().join() !== 'sha256,size' || !Number.isSafeInteger(p.source.size) || p.source.size <= 0 || !hash(p.source.sha256) ||
      !validPreview(p.preview as PlanResponse,p.project_slug) || (p.preview.environment ?? null) !== p.environment || fingerprint(p) !== p.fingerprint || Buffer.byteLength(stable(p)) > 4*1024*1024) throw new Error();
    return snapshot(p);
  } catch { throw new ProjectWorkflowError('invalid_plan','previewing'); }
}
const stages = ['prepared','applying','accepted','rejected','complete'];
const rejections: ProjectWorkflowCode[] = ['preview_expired','plan_stale','permission_denied','blocked'];
export function parseProjectApplicationState(value: unknown): ProjectApplicationState {
  const s = value as ProjectApplicationState;
  if (!s || Object.keys(s).sort().join() !== 'attempt_id,base_url,environment,plan_fingerprint,project_slug,rejection,stage,version' || s.version !== 1 || base(s.base_url) !== s.base_url || !slug(s.project_slug) ||
    s.environment !== null && !slug(s.environment) || !hash(s.plan_fingerprint) || typeof s.attempt_id !== 'string' || !/^[a-f0-9]{32}$/.test(s.attempt_id) || !stages.includes(s.stage) ||
    (s.stage === 'rejected' ? !s.rejection || !rejections.includes(s.rejection) : s.rejection !== null)) throw new TypeError('Invalid project application state');
  return Object.freeze({...s});
}
async function canonical(path: string): Promise<string> { return join(await realpath(dirname(resolve(path))),basename(path)); }
export async function loadProjectApplicationState(path: string): Promise<ProjectApplicationState> {
  const file = await open(await canonical(path),constants.O_RDONLY | (constants.O_NOFOLLOW??0) | (constants.O_NONBLOCK??0));
  try {
    const info = await file.stat(); if (!info.isFile() || info.size > 65536) throw new TypeError('Invalid project application state');
    const data=Buffer.alloc(65537), {bytesRead}=await file.read(data,0,data.length,0);
    if (bytesRead>65536) throw new TypeError('Invalid project application state');
    return parseProjectApplicationState(JSON.parse(data.subarray(0,bytesRead).toString()));
  } finally { await file.close(); }
}
async function save(path: string, state: ProjectApplicationState): Promise<void> {
  const temporary=join(dirname(path),'.'+basename(path)+'.'+randomUUID()), file=await open(temporary,'wx',0o600);
  try {
    try { await file.writeFile(JSON.stringify(state)); await file.sync(); } finally { await file.close(); }
    await rename(temporary,path);
    if(process.platform!=='win32'){const directory=await open(dirname(path),'r');try{await directory.sync();}finally{await directory.close();}}
  } finally { await unlink(temporary).catch(e=>{if(e.code!=='ENOENT')throw e;}); }
}
function status(error: unknown): number | undefined { return (error as {status?:number})?.status; }
function refusal(error: unknown): ProjectWorkflowCode | undefined {
  const code=(error as {problem?:{code?:string}})?.problem?.code;
  return status(error)===410?'preview_expired':code==='plan_token_stale'?'plan_stale':status(error)===401||status(error)===403?'permission_denied':status(error)===409?'blocked':undefined;
}
class Operation {
  readonly controller=new AbortController(); readonly start=performance.now();
  phase:ProjectWorkflowPhase='preparing'; state?:ProjectApplicationState;
  constructor(readonly config:ClientConfig,readonly project:string,readonly options:ProjectWorkflowOptions){
    if(!options||!Number.isInteger(options.timeoutMs)||options.timeoutMs<1||options.timeoutMs>2147483647||!slug(project)||options.onProgress!==undefined&&typeof options.onProgress!=='function')throw new TypeError('A project slug and positive total timeoutMs are required');
    this.config={...config,requestFetch:policy=>{const send=config.requestFetch(policy);return (url,init)=>{this.check();return send(url,{...init,redirect:'error'});};}};
  }
  error(code:ProjectWorkflowCode,cause?:unknown){return new ProjectWorkflowError(code,this.phase,this.state,cause);}
  check(){this.controller.signal.throwIfAborted();if(performance.now()-this.start>=this.options.timeoutMs)throw this.error('timeout');}
  remaining(){this.check();return Math.max(1,Math.ceil(this.options.timeoutMs-(performance.now()-this.start)));}
  async event(detail:Partial<ProjectWorkflowProgress>={}){
    this.check();let stop!:()=>void;const signal=this.controller.signal;
    try{await Promise.race([Promise.resolve().then(()=>{this.check();return this.options.onProgress?.(Object.freeze({...detail,phase:this.phase,projectSlug:this.project,attemptId:this.state?.attempt_id??null,elapsedMs:performance.now()-this.start}));}),new Promise<never>((_,reject)=>{stop=()=>reject(signal.reason);signal.addEventListener('abort',stop,{once:true});if(signal.aborted)stop();})]);}
    catch(error){this.check();throw this.error('progress_failed',error);}finally{signal.removeEventListener('abort',stop);}this.check();
  }
  async request<T>(method:'GET'|'POST',url:string,body?:unknown,headers?:Record<string,string>,formData?:Record<string,unknown>):Promise<T>{
    this.check();return scopedRequest<T>(this.config,{method,url,body,mediaType:body===undefined?undefined:'application/json',headers,formData},{timeoutMs:this.remaining(),signal:this.controller.signal,retry:method==='POST'?false:this.options.retry,onResponse:this.options.onResponse});
  }
  async run<T>(work:()=>Promise<T>):Promise<T>{
    const abort=()=>this.controller.abort(this.error('cancelled')),caller=this.options.signal;
    caller?.addEventListener('abort',abort,{once:true});if(caller?.aborted)abort();
    const timer=setTimeout(()=>this.controller.abort(this.error('timeout')),this.options.timeoutMs);
    let stop!:()=>void;
    const cancelled=new Promise<never>((_,reject)=>{stop=()=>reject(this.controller.signal.reason);this.controller.signal.addEventListener('abort',stop,{once:true});if(this.controller.signal.aborted)stop();});
    const running=work();
    try{return await Promise.race([running,cancelled]);}catch(error){if(error instanceof ProjectWorkflowError)throw error;this.check();throw this.error(this.phase==='previewing'||this.phase==='preparing'?'preview_failed':'acceptance_unknown',error);}
    finally{clearTimeout(timer);caller?.removeEventListener('abort',abort);this.controller.signal.removeEventListener('abort',stop);this.controller.abort(this.error('cancelled'));}
  }
}
export function bindProjectWorkflows(deployments:Parameters<typeof bindDeploymentWaiters>[0],getConfig:()=>ClientConfig):ProjectWorkflows{
  const apply=(original:ReviewedProjectPlan,raw:ProjectApplyOptions,resume:boolean):Promise<ProjectApplicationResult>=>{
    const plan=parseReviewedProjectPlan(original),options={...raw,wait:raw.wait&&{...raw.wait},retry:raw.retry&&{...raw.retry},initialSecrets:raw.initialSecrets&&structuredClone(raw.initialSecrets)};
    if(typeof options.statePath!=='string'||!options.statePath)throw new TypeError('statePath is required');
    const max=options.maxReceiptPolls??30,min=options.wait?.minDelayMs??1000,maxDelay=options.wait?.maxDelayMs??5000;
    if(!Number.isSafeInteger(max)||max<1||max>10000||![min,maxDelay].every(n=>Number.isInteger(n)&&n>0&&n<=2147483647)||min>maxDelay||options.wait?.maxPolls!==undefined&&(!Number.isSafeInteger(options.wait.maxPolls)||options.wait.maxPolls<1)||options.wait?.stopOnBlocked!==undefined&&typeof options.wait.stopOnBlocked!=='boolean')throw new TypeError('Invalid project observation policy');
    const op=new Operation(getConfig(),plan.project_slug,options);let path:string|undefined,held=false;
    return op.run(async()=>{
      try{
        if(op.config.BASE!==plan.base_url)throw op.error('state_mismatch');
        op.check();path=await canonical(options.statePath);op.check();if(active.has(path))throw op.error('busy');active.add(path);held=true;
        try{op.state=await loadProjectApplicationState(path);}catch(error){if((error as NodeJS.ErrnoException).code!=='ENOENT'||resume)throw op.error('invalid_state',error);}
        const persist=async(state:ProjectApplicationState)=>{op.check();try{await save(path!,state);}catch(error){throw op.error('state_failed',error);}op.state=state;op.check();};
        if(op.state){if(op.state.base_url!==plan.base_url||op.state.plan_fingerprint!==plan.fingerprint||op.state.project_slug!==plan.project_slug||op.state.environment!==plan.environment)throw op.error('state_mismatch');}
        else await persist(parseProjectApplicationState({version:1,base_url:plan.base_url,project_slug:plan.project_slug,environment:plan.environment,plan_fingerprint:plan.fingerprint,attempt_id:randomUUID().replaceAll('-',''),stage:'prepared',rejection:null}));
        if(op.state!.stage==='rejected')throw op.error(op.state!.rejection!);
        if(op.state!.stage==='prepared'){
          if(!plan.preview.can_apply)throw op.error('blocked');
          op.phase='applying';await persist({...op.state!,stage:'applying'});await op.event();
          try{
            const response=await op.request<ApplyResponse>('POST','/v1/projects/apply-plan',{plan_token:plan.preview.plan_token,...(options.approvalToken?{approval_token:options.approvalToken}:{}),...(options.initialSecrets?{initial_secrets:options.initialSecrets}:{})},{'Deployment-Attempt-ID':op.state!.attempt_id,'Idempotency-Key':op.state!.attempt_id});
            if(response?.submission_attempt_id!==op.state!.attempt_id||response.project_slug!==plan.project_slug)throw op.error('acceptance_unknown');
            await persist({...op.state!,stage:'accepted'});
          }catch(error){op.check();if(error instanceof ProjectWorkflowError)throw error;const code=refusal(error);if(code){await persist({...op.state!,stage:'rejected',rejection:code});throw op.error(code,error);}/* Uncertain response: recover below, never repeat POST. */}
        }
        op.phase='recovering';let receipt:ProjectSubmissionReceipt|undefined;
        for(let poll=1;poll<=max;poll++){
          await op.event();
          try{receipt=await op.request<ProjectSubmissionReceipt>('GET','/v1/project-submissions/'+op.state!.attempt_id);}catch(error){op.check();throw op.error('acceptance_unknown',error);}
          if(!receipt||receipt.attempt_id!==op.state!.attempt_id||receipt.project_slug!==plan.project_slug||(receipt.environment??null)!==plan.environment||typeof receipt.selection_complete!=='boolean'||!['processing','complete','rejected','interrupted'].includes(receipt.status)||!receipt.apply||receipt.apply.project_slug&&receipt.apply.project_slug!==plan.project_slug||receipt.apply.submission_attempt_id&&receipt.apply.submission_attempt_id!==op.state!.attempt_id)throw op.error('invalid_response');
          if(receipt.status!=='processing')break;
          if(poll===max)throw op.error('poll_limit');
          await new Promise<void>((resolve,reject)=>{const timer=setTimeout(done,Math.min(maxDelay,min*2**Math.min(poll-1,20)));const signal=op.controller.signal;function done(){signal.removeEventListener('abort',stop);resolve();}function stop(){clearTimeout(timer);reject(signal.reason);}signal.addEventListener('abort',stop,{once:true});if(signal.aborted)stop();});
        }
        if(!receipt!.selection_complete)throw op.error('acceptance_unknown');
        await persist({...op.state!,stage:'accepted'});op.phase='observing';
        const results:ProjectWorkloadOutcome[]=[],apps=receipt!.apply.apps??[],rows=receipt!.apply.builds??[],unchanged=new Set(receipt!.apply.unchanged??[]),seen=new Set<string>(),ids=new Set<string>();
        for(const row of rows){if(!row||!slug(row.slug)||!row.app_id||seen.has(row.slug)||row.deployment_id&&ids.has(row.deployment_id)||!apps.some(a=>a.slug===row.slug&&a.id===row.app_id)||row.build_id&&!row.deployment_id||false)throw op.error('invalid_response');seen.add(row.slug);if(row.deployment_id)ids.add(row.deployment_id);}
        const waiters=bindDeploymentWaiters(deployments,()=>op.config);
        for(const app of apps){
          if(!slug(app.slug)||!app.id||apps.filter(a=>a.slug===app.slug).length!==1)throw op.error('invalid_response');
          const row=rows.find(r=>r.slug===app.slug);let result:ProjectWorkloadOutcome={slug:app.slug,appId:app.id,deploymentId:row?.deployment_id??null,buildId:row?.build_id??null,outcome:'unknown',code:'submission_incomplete'};
          if(row?.error&&!row.deployment_id)result={...result,outcome:'failed',code:'enqueue_failed'};
          else if(row?.deployment_id){
            try{const timeline=await waiters.waitForDeployment({id:row.deployment_id},{...options.wait,timeoutMs:op.remaining(),signal:op.controller.signal,retry:options.retry,onResponse:options.onResponse,onProgress:()=>op.event({workload:app.slug})});result={...result,code:row.error?'post_acceptance_failed':undefined,outcome:row.error?'failed':'succeeded',timeline:snapshot(timeline)};}
            catch(error){op.check();if(!(error instanceof WaiterError))throw error;if(error.code==='timeout')throw op.error('timeout',error);if(error.code==='poll_limit')throw op.error('poll_limit',error);result={...result,outcome:error.code==='blocked'?'blocked':'failed',code:error.code,timeline:error.lastObserved as WaiterSnapshot<ReleaseTimeline>|undefined};}
          }else if(unchanged.has(app.slug))result={...result,outcome:'unchanged',code:undefined};
          results.push(Object.freeze(result));await op.event({workload:app.slug,outcome:result.outcome});
        }
        if(rows.length>results.length)throw op.error('invalid_response');
        const unchangedReview=new Set([...(plan.preview.unaffected??[]),...(plan.preview.skipped??[]),...(plan.preview.will_deploy??[]).filter(r=>r.action==='noop')].map(r=>r.slug));
        for(const reviewed of [...(plan.preview.will_deploy??[]),...(plan.preview.unaffected??[]),...(plan.preview.skipped??[])])if(!results.some(r=>r.slug===reviewed.slug)){
          const keep=unchangedReview.has(reviewed.slug)||(!!reviewed.id&&receipt!.status==='complete');
          results.push(Object.freeze({slug:reviewed.slug,appId:reviewed.id??null,deploymentId:null,buildId:null,outcome:keep?'unchanged':'unknown',...(keep?{}:{code:'submission_incomplete'})}));
        }

        const success=results.some(r=>r.outcome==='succeeded'||r.outcome==='unchanged'),bad=results.some(r=>r.outcome==='failed'||r.outcome==='unknown')||receipt!.status!=='complete',blocked=results.some(r=>r.outcome==='blocked');
        const outcome=bad?(success?'partial':'failed'):blocked?'blocked':'succeeded';
        await persist({...op.state!,stage:'complete'});op.phase='complete';await op.event();
        return Object.freeze({outcome,attemptId:op.state!.attempt_id,statePath:path!,receipt:snapshot({...receipt!,apply:normalize(receipt!.apply)}),workloads:Object.freeze(results)});
      }finally{if(held)active.delete(path!);}
    });
  };
  return {
    async previewLocal(raw,options){
      const input={...raw,only:raw.only&&[...raw.only],appSlugs:raw.appSlugs&&{...raw.appSlugs}},policy={...options,retry:options.retry&&{...options.retry},preparation:options.preparation&&{...options.preparation,exclude:options.preparation.exclude&&[...options.preparation.exclude]}};
      if(input.environment!==undefined&&!slug(input.environment)||input.only?.some(s=>typeof s!=='string'||!s||s.includes(',')||/[\x00-\x1f]/.test(s))||input.noTriggers!==undefined&&typeof input.noTriggers!=='boolean'||input.appSlugs&&Object.entries(input.appSlugs).some(([key,value])=>!key||!slug(value)))throw new TypeError('Invalid project preview selection');
      const op=new Operation(getConfig(),input.projectSlug,policy);
      return op.run(async()=>{
        const temporary=await mkdtemp(join(tmpdir(),'gregale-project-preview-'));
        try{
          await op.event();const source=await prepareSource(input.directory,join(temporary,'source.tar.gz'),{...policy.preparation,sourceRoot:'.',timeoutMs:op.remaining(),signal:op.controller.signal,onProgress:()=>op.event()});
          op.phase='previewing';await op.event();const blob=await openAsBlob(source.path,{type:'application/gzip'});
          const preview=normalize(await op.request<PlanResponse>('POST','/v1/projects/scan',undefined,undefined,{source:new File([blob],'source.tar.gz',{type:'application/gzip'}),project_slug:input.projectSlug,...(input.appSlugs?{app_slugs:input.appSlugs}:{}),...(input.environment?{environment:input.environment}:{}),...(input.only?{only:input.only.join(',')}:{}),...(input.noTriggers!==undefined?{no_triggers:input.noTriggers}:{})}));
          if(!validPreview(preview,input.projectSlug)||(preview.environment??null)!==(input.environment??null))throw op.error('invalid_response');
          const plan:ReviewedProjectPlan={version:1,base_url:op.config.BASE,project_slug:input.projectSlug,environment:input.environment??null,source:{size:source.size,sha256:source.sha256},preview:snapshot(preview),fingerprint:''};
          return parseReviewedProjectPlan({...plan,fingerprint:fingerprint(plan)});
        }finally{await rm(temporary,{recursive:true,force:true});}
      });
    },
    applyPlan:(plan,options)=>apply(plan,options,false),
    resumePlanApplication:(plan,options)=>apply(plan,options,true),
  };
}
