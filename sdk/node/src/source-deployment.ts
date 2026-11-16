/** Durable composition of local preparation, source transfer and release observation. */
import { constants } from 'node:fs';
import { open, realpath, rename, unlink } from 'node:fs/promises';
import { basename, dirname, join, resolve, relative, isAbsolute, sep } from 'node:path';
import { randomUUID, createHash } from 'node:crypto';
import type { ClientConfig } from './client-instance.js';
import { bindSourceUploads, parseSourceUploadCheckpoint, sourceUploadMetadataHash, SourceUploadError, type SourceUploadCheckpoint, type SourceUploadProgress } from './source-upload.js';
import { prepareSourceWithFiles, SourcePreparationError, type SourcePreparationOptions, type SourcePreparationProgress } from './source-preparation.js';
import { parseProjectPlan, projectBuildFiles, projectDeployOptions, validateProjectPlan, ProjectInspectionError, type ProjectPlan } from './project-inspection.js';
import { bindDeploymentWaiters, snapshot, WaiterError, type DeploymentWaitOptions, type WaiterFailureCode, type WaiterSnapshot } from './deployment-waiters.js';
import type { RequestOptions } from './request-options.js';
import type { UploadDeployOptions } from './generated/models/UploadDeployOptions.js';
import type { DeploymentSubmissionReceipt } from './generated/models/DeploymentSubmissionReceipt.js';
import type { ReleaseTimeline } from './generated/models/ReleaseTimeline.js';

export type SourceDeploymentPhase = 'preparing' | 'uploading' | 'committing' | 'observing' | 'complete';
export type SourceDeploymentCode = 'invalid_state' | 'state_mismatch' | 'busy' | 'state_failed' | 'preparation_failed' | 'upload_failed' | 'acceptance_unknown' | 'wait_failed' | 'progress_failed' | 'timeout' | 'cancelled';
export interface SourceDeploymentArchive { readonly size: number; readonly sha256: string; }
export type SourceDeploymentPreparationOptions = Omit<SourcePreparationOptions,'sourceRoot'|'timeoutMs'|'signal'|'onProgress'>;
export type SourceDeploymentWaitOptions = Pick<DeploymentWaitOptions,'minDelayMs'|'maxDelayMs'|'maxPolls'|'stopOnBlocked'>;
export interface SourceDeploymentReleaseProgress { readonly phase: string; readonly status: string; readonly trafficPercent: number; readonly poll: number; }
/** Portable, private local state. No credentials, deployment metadata values or release logs. */
export interface SourceDeploymentState {
  readonly version: 1; readonly workflow_id: string; readonly base_url: string; readonly app_slug: string;
  readonly directory: string; readonly metadata_sha256: string; readonly preparation_sha256: string;
  readonly archive: SourceDeploymentArchive | null; readonly upload: SourceUploadCheckpoint | null;
}
export interface SourceDeploymentProgress {
  readonly workflowId: string; readonly phase: SourceDeploymentPhase; readonly elapsedMs: number;
  readonly source?: SourcePreparationProgress; readonly upload?: SourceUploadProgress;
  readonly release?: SourceDeploymentReleaseProgress;
}
export interface SourceDeploymentOptions extends Omit<RequestOptions,'timeoutMs'> {
  readonly statePath: string; readonly timeoutMs: number;
  /** sourceRoot comes from deployOptions.source_root. Time and progress belong to the workflow. */
  readonly preparation?: SourceDeploymentPreparationOptions;
  readonly wait?: SourceDeploymentWaitOptions;
  readonly maxRecoveries?: number;
  readonly project?:ProjectPlan;
  readonly onProgress?: (event: SourceDeploymentProgress) => void | Promise<void>;
}
export interface SourceDeploymentInput { readonly directory: string; readonly appSlug: string; readonly deployOptions?: UploadDeployOptions; }
export interface SourceDeploymentResumeInput { readonly appSlug: string; readonly deployOptions?: UploadDeployOptions; }
export interface SourceDeploymentResult {
  readonly outcome: 'succeeded' | 'blocked' | 'failed'; readonly workflowId: string; readonly statePath: string;
  readonly deploymentId: string; readonly receipt: Readonly<DeploymentSubmissionReceipt>;
  readonly timeline: WaiterSnapshot<ReleaseTimeline>; readonly code?: WaiterFailureCode;
}
export class SourceDeploymentError extends Error {
  readonly outcome: 'failed' | 'timed_out' | 'cancelled' | 'acceptance_unknown';
  constructor(readonly code: SourceDeploymentCode, readonly phase: SourceDeploymentPhase, readonly statePath: string,
    readonly state?: SourceDeploymentState, override readonly cause?: unknown) {
    super(`SDK source deployment stopped: ${code}`); this.name='SourceDeploymentError';
    this.outcome=code==='timeout'?'timed_out':code==='cancelled'?'cancelled':code==='acceptance_unknown'?'acceptance_unknown':'failed';
  }
}
export interface SourceDeployments {
  deploySource(input: SourceDeploymentInput, options: SourceDeploymentOptions): Promise<SourceDeploymentResult>;
  resumeSourceDeployment(input: SourceDeploymentResumeInput, options: SourceDeploymentOptions): Promise<SourceDeploymentResult>;
  deployProject(project:ProjectPlan, options:SourceDeploymentOptions):Promise<SourceDeploymentResult>;
  resumeProjectDeployment(project:ProjectPlan, options:SourceDeploymentOptions):Promise<SourceDeploymentResult>;
}
const active=new Set<string>(); // A journal has one writer; coordinate separate processes externally.
const digest=(value: string)=>createHash('sha256').update(value).digest('hex');
const hash=(value:unknown):value is string=>typeof value==='string' && /^[a-f0-9]{64}$/.test(value);
const uuid=(value:unknown):value is string=>typeof value==='string' && /^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/.test(value);
const exact=(value:object,keys:string[])=>Object.keys(value).length===keys.length && keys.every(k=>Object.hasOwn(value,k));
export function parseSourceDeploymentState(value:unknown):SourceDeploymentState {
  const bad=():never=>{ throw new TypeError('Invalid source deployment state'); };
  if (!value || typeof value!=='object') return bad();
  const s=value as SourceDeploymentState;
  if (!exact(s,['version','workflow_id','base_url','app_slug','directory','metadata_sha256','preparation_sha256','archive','upload']) || s.version!==1 || !uuid(s.workflow_id) ||
    typeof s.base_url!=='string' || typeof s.app_slug!=='string' || !/^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/.test(s.app_slug) ||
    typeof s.directory!=='string' || !isAbsolute(s.directory) || !hash(s.metadata_sha256) || !hash(s.preparation_sha256)) return bad();
  const url=new URL(s.base_url);
  if (!['http:','https:'].includes(url.protocol) || url.username || url.password || url.search || url.hash || url.href.replace(/\/+$/,'')!==s.base_url) return bad();
  if (s.archive!==null && (!s.archive || !exact(s.archive,['size','sha256']) || !Number.isSafeInteger(s.archive.size) || s.archive.size<=0 || !hash(s.archive.sha256))) return bad();
  const upload=s.upload===null?null:parseSourceUploadCheckpoint(s.upload);
  if (upload && (!s.archive || upload.base_url!==s.base_url || upload.app_slug!==s.app_slug || upload.options_sha256!==s.metadata_sha256 || upload.total_size!==s.archive.size || upload.sha256_hex!==s.archive.sha256)) return bad();
  return Object.freeze({...s,archive:s.archive && Object.freeze({...s.archive}),upload});
}
async function canonicalPath(path:string):Promise<string> { return join(await realpath(dirname(resolve(path))),basename(path)); }
async function readState(path:string):Promise<SourceDeploymentState> {
  const file=await open(path,constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0) | (constants.O_NONBLOCK ?? 0));
  try { const stat=await file.stat(); if (!stat.isFile() || stat.size>65536) throw new TypeError('Invalid source deployment state'); const buffer=Buffer.alloc(65537); const {bytesRead}=await file.read(buffer,0,buffer.length,0); if(bytesRead>65536)throw new TypeError('Invalid source deployment state'); return parseSourceDeploymentState(JSON.parse(buffer.subarray(0,bytesRead).toString('utf8'))); }
  finally { await file.close(); }
}
export async function loadSourceDeploymentState(path:string):Promise<SourceDeploymentState> { return readState(await canonicalPath(path)); }
async function writeState(path:string,state:SourceDeploymentState):Promise<void> {
  const text=JSON.stringify(state);if(Buffer.byteLength(text)>65536)throw new TypeError('Source deployment state exceeds 64 KiB');
  const temporary=join(dirname(path),'.'+basename(path)+'.'+randomUUID()); const file=await open(temporary,'wx',0o600);
  try {
    try { await file.writeFile(text); await file.sync(); } finally { await file.close(); }
    await rename(temporary,path);
    if (process.platform!=='win32') { const parent=await open(dirname(path),'r'); try { await parent.sync(); } finally { await parent.close(); } }
  } finally { await unlink(temporary).catch(e=>{if(e.code!=='ENOENT') throw e;}); }
}
function preparationPolicy(options:SourceDeploymentOptions,metadata:UploadDeployOptions|undefined) {
  const p=options.preparation;
  return {sourceRoot:metadata?.source_root ?? '.',dockerfile:p?.dockerfile ?? null,exclude:[...new Set(p?.exclude ?? [])].sort((a,b)=>Buffer.compare(Buffer.from(a),Buffer.from(b))),
    secretScan:p?.secretScan ?? 'strict',maxEntries:p?.maxEntries ?? 10000,maxBytes:p?.maxBytes ?? 2*1024**3,maxArchiveBytes:p?.maxArchiveBytes ?? Number.MAX_SAFE_INTEGER,...(options.project?{projectFingerprint:options.project.fingerprint}:{})};
}
class Workflow {
  readonly options:SourceDeploymentOptions; readonly controller=new AbortController(); readonly start=performance.now();
  state?:SourceDeploymentState; phase:SourceDeploymentPhase='preparing'; path:string; writes:Promise<void>=Promise.resolve();
  constructor(readonly config:ClientConfig,readonly input:SourceDeploymentResumeInput,options:SourceDeploymentOptions) {
    if (!options || typeof options.statePath!=='string' || !options.statePath || !Number.isInteger(options.timeoutMs) || options.timeoutMs<=0 || options.timeoutMs>2147483647 ||
      options.onProgress!==undefined && typeof options.onProgress!=='function') throw new TypeError('A statePath and positive total timeoutMs are required');
    this.path=resolve(options.statePath);
    const wait=options.wait,min=wait?.minDelayMs ?? 1000,max=wait?.maxDelayMs ?? 5000;
    if(![min,max].every(v=>Number.isInteger(v) && v>0 && v<=2147483647) || min>max ||
      wait?.maxPolls!==undefined && (!Number.isSafeInteger(wait.maxPolls) || wait.maxPolls<1) || wait?.stopOnBlocked!==undefined && typeof wait.stopOnBlocked!=='boolean' ||
      !Number.isInteger(options.maxRecoveries ?? 3) || (options.maxRecoveries ?? 3)<0 || (options.maxRecoveries ?? 3)>100 || !/^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/.test(input.appSlug))throw new TypeError('Invalid deployment workflow policy');
    this.options={...options,retry:options.retry && {...options.retry},preparation:options.preparation && {...options.preparation,exclude:[...(options.preparation.exclude ?? [])]},wait:options.wait && {...options.wait}};
    if(options.project){const project=parseProjectPlan(options.project);this.options={...this.options,project,preparation:{...this.options.preparation,dockerfile:project.dockerfile}};}
    this.config={...config,requestFetch:policy=>{
      const send=config.requestFetch(policy);
      return (url,init)=>{this.check();return send(url,init);};
    }};
  }
  error(code:SourceDeploymentCode,cause?:unknown) { return new SourceDeploymentError(code,this.phase,this.path,this.state,cause); }
  check() { if (performance.now()-this.start>=this.options.timeoutMs && !this.controller.signal.aborted) this.controller.abort(this.error('timeout')); this.controller.signal.throwIfAborted(); }
  remaining() { this.check(); return Math.max(1,Math.ceil(this.options.timeoutMs-(performance.now()-this.start))); }
  async save(value:SourceDeploymentState) {
    this.check(); const state=parseSourceDeploymentState(value);
    const write=this.writes.then(async()=>{this.check(); try { await writeState(this.path,state); } catch(error) { throw this.error('state_failed',error); } this.state=state; this.check();});
    this.writes=write.catch(()=>{}); await write;
  }
  async event(detail:Partial<SourceDeploymentProgress>={}) {
    this.check(); if (!this.options.onProgress) return;
    const event=Object.freeze({...detail,workflowId:this.state!.workflow_id,phase:this.phase,elapsedMs:performance.now()-this.start});
    const signal=this.controller.signal; let stop!:()=>void;
    try {
      await Promise.race([Promise.resolve().then(()=>{this.check();return this.options.onProgress!(event);}),new Promise<never>((_,reject)=>{stop=()=>reject(signal.reason);signal.addEventListener('abort',stop,{once:true});if(signal.aborted)stop();})]);
    } catch(error) { this.check(); throw this.error('progress_failed',error); }
    finally { signal.removeEventListener('abort',stop); }
    this.check();
  }
}
export function bindSourceDeployments(service:Parameters<typeof bindDeploymentWaiters>[0],getConfig:()=>ClientConfig):SourceDeployments {
  const execute=(input:SourceDeploymentResumeInput,options:SourceDeploymentOptions,directory?:string):Promise<SourceDeploymentResult>=> {
    const metadata=structuredClone(input.deployOptions),captured={appSlug:input.appSlug,deployOptions:metadata};
    const op=new Workflow(getConfig(),captured,options),source=directory===undefined?undefined:resolve(directory);
    const signal=op.options.signal;
    const callerAbort=()=>op.controller.abort(op.error('cancelled',signal?.reason));
    const timer=setTimeout(()=>op.controller.abort(op.error('timeout')),op.options.timeoutMs);
    signal?.addEventListener('abort',callerAbort,{once:true}); if(signal?.aborted)callerAbort();
    let held=false;
    const work=async():Promise<SourceDeploymentResult>=> {
      try {
        op.check(); op.path=await canonicalPath(op.path); op.check();
        if(active.has(op.path))throw op.error('busy'); active.add(op.path); held=true;
        try { op.state=await readState(op.path); }
        catch(error) {
          if((error as NodeJS.ErrnoException).code!=='ENOENT')throw op.error('invalid_state',error);
          if(source===undefined)throw op.error('invalid_state',error);
        }
        op.check(); const prep=preparationPolicy(op.options,metadata),metadataHash=sourceUploadMetadataHash(metadata),prepHash=digest(JSON.stringify(prep));
        const project=op.options.project;
        if(project){const expected=projectDeployOptions(project);if(project.issues.some(i=>i.severity==='error')||project.appSlug!==null&&project.appSlug!==captured.appSlug||source!==undefined&&source!==resolve(project.contextRoot)||Object.entries(expected).some(([k,v])=>(metadata as Record<string,unknown>|undefined)?.[k]!==v)||metadata?.runtime!==expected.runtime||metadata?.handler!==expected.handler||Boolean(metadata?.dockerfile)!==project.dockerfile)throw op.error('state_mismatch');}
        if(op.state) {
          if(op.state.base_url!==op.config.BASE || op.state.app_slug!==captured.appSlug || op.state.metadata_sha256!==metadataHash || op.state.preparation_sha256!==prepHash || source!==undefined && op.state.directory!==source)throw op.error('state_mismatch');
        } else {
          const root=await realpath(source!),rel=relative(root,dirname(op.path));
          if(rel==='' || rel!=='..' && !rel.startsWith('..'+sep) && !isAbsolute(rel))throw op.error('state_mismatch',new TypeError('Workflow state must be outside the source context'));
          if(project){const rel=relative(project.projectRoot,dirname(op.path));if(rel===''||rel!=='..'&&!rel.startsWith('..'+sep)&&!isAbsolute(rel))throw op.error('state_mismatch',new TypeError('Project journal must be outside the inspected project'));}
          await op.save({version:1,workflow_id:randomUUID(),base_url:op.config.BASE,app_slug:captured.appSlug,directory:source!,metadata_sha256:metadataHash,preparation_sha256:prepHash,archive:null,upload:null});
        }
        const archive=join(dirname(op.path),`.gregale-deploy-${op.state!.workflow_id}.tar.gz`);
        if(!op.state!.archive) {
          op.phase='preparing'; await op.event();
          if(project)await validateProjectPlan(project,{timeoutMs:op.remaining(),signal:op.controller.signal});
          const prepared=await prepareSourceWithFiles(op.state!.directory,archive,{...op.options.preparation,sourceRoot:prep.sourceRoot,timeoutMs:op.remaining(),signal:op.controller.signal,onProgress:event=>op.event({source:event})},project?projectBuildFiles(project):{});
          if(project){await validateProjectPlan(project,{timeoutMs:op.remaining(),signal:op.controller.signal});for(const input of project.inputs){const path=resolve(project.projectRoot,input.path),rel=relative(project.contextRoot,path).split(sep).join('/');if(input.kind==='file'&&input.sha256!==null&&rel&&!rel.startsWith('../')&&!isAbsolute(rel)&&!prepared.plan.entries.some(e=>e.path===rel&&e.included))throw new ProjectInspectionError('invalid_plan','An inspected build input is excluded from the archive');}}
          await op.save({...op.state!,archive:{size:prepared.size,sha256:prepared.sha256}});
        }
        const uploads=bindSourceUploads(()=>op.config);
        const uploadOptions=()=>({timeoutMs:op.remaining(),signal:op.controller.signal,retry:op.options.retry,onResponse:op.options.onResponse,maxRecoveries:op.options.maxRecoveries,
          onCheckpoint:async (upload:SourceUploadCheckpoint)=> { op.check(); await op.save({...op.state!,upload}); },
          onProgress:(upload:SourceUploadProgress)=>op.event({upload})});
        if(!op.state!.upload || !['committing','accepted'].includes(op.state!.upload!.stage)) {
          op.phase='uploading'; await op.event();
          if(!op.state!.upload) await op.save({...op.state!,upload:parseSourceUploadCheckpoint({version:1,base_url:op.state!.base_url,app_slug:captured.appSlug,
            total_size:op.state!.archive!.size,sha256_hex:op.state!.archive!.sha256,options_sha256:op.state!.metadata_sha256,start_key:randomUUID(),attempt_id:randomUUID(),commit_key:randomUUID(),
            stage:'new',upload_id:null,chunk_size:0,received_bytes:0,pending_end:null,deployment_id:null,expires_at:null})});
          if(op.state!.upload!.stage==='new')await op.event({upload:Object.freeze({stage:'new',received_bytes:0,total_size:op.state!.archive!.size})});
          await uploads.uploadSource({path:archive,appSlug:captured.appSlug,deployOptions:metadata,checkpoint:op.state!.upload ?? undefined},uploadOptions());
        }
        op.phase='committing'; await op.event();
        const receipt=await uploads.commitSource({checkpoint:op.state!.upload!},uploadOptions());
        op.phase='observing'; await op.event();
        let timeline:WaiterSnapshot<ReleaseTimeline>|undefined,code:WaiterFailureCode|undefined;
        try {
          const value=await bindDeploymentWaiters(service,()=>op.config).waitForDeployment({id:receipt.deployment_id},{...op.options.wait,timeoutMs:op.remaining(),signal:op.controller.signal,retry:op.options.retry,onResponse:op.options.onResponse,
            onProgress:(value,progress)=>op.event({release:Object.freeze({phase:value.phase,status:value.status,trafficPercent:value.traffic_percent,poll:progress.poll})})});
          timeline=snapshot(value);
        } catch(error) {
          op.check();
          if(!(error instanceof WaiterError) || !error.lastObserved || !['failed','cancelled','rolled_back','superseded','blocked','not_serving','regressed'].includes(error.code))throw error;
          code=error.code; timeline=error.lastObserved as WaiterSnapshot<ReleaseTimeline>|undefined;
        }
        op.phase='complete'; await op.event();
        return Object.freeze({outcome:code==='blocked'?'blocked':code?'failed':'succeeded',workflowId:op.state!.workflow_id,statePath:op.path,deploymentId:receipt.deployment_id,receipt:Object.freeze({...receipt}),timeline:timeline!,code});
      } catch(error) {
        if(error instanceof SourceDeploymentError)throw error;
        op.check();
        if(error instanceof SourceUploadError && error.cause instanceof SourceDeploymentError)throw error.cause;
        if(error instanceof SourceUploadError && error.code==='acceptance_unknown')throw op.error('acceptance_unknown',error);
        if(error instanceof SourceUploadError && error.code==='checkpoint_failed')throw op.error('state_failed',error);
        if(error instanceof SourcePreparationError && error.code==='deadline_exceeded' || error instanceof SourceUploadError && error.code==='timeout' || error instanceof WaiterError && error.code==='timeout')throw op.error('timeout',error);
        throw op.error(op.phase==='preparing'?'preparation_failed':op.phase==='observing'?'wait_failed':'upload_failed',error);
      } finally {
        await op.writes;
        if(held)active.delete(op.path);
      }
    };
    const running=work();
    let stop!:()=>void;
    const cancelled=new Promise<never>((_,reject)=>{stop=()=>reject(op.controller.signal.reason);op.controller.signal.addEventListener('abort',stop,{once:true});if(op.controller.signal.aborted)stop();});
    const result=Promise.race([running,cancelled]);
    return result.finally(()=>{clearTimeout(timer);signal?.removeEventListener('abort',callerAbort);op.controller.signal.removeEventListener('abort',stop);op.controller.abort(op.error('cancelled'));});
  };
  const projectInput=(project:ProjectPlan,options:SourceDeploymentOptions)=>{const p=parseProjectPlan(project);if(!p.appSlug)throw new ProjectInspectionError('invalid_plan','Set appSlug when inspecting a project for deployment');return {input:{appSlug:p.appSlug,deployOptions:projectDeployOptions(p)},options:{...options,project:p},directory:p.contextRoot};};
  return {deploySource:async(input,options)=>execute(input,options,input.directory),resumeSourceDeployment:async(input,options)=>execute(input,options),
    deployProject:async(project,options)=>{const p=projectInput(project,options);return execute(p.input,p.options,p.directory);},resumeProjectDeployment:async(project,options)=>{const p=projectInput(project,options);return execute(p.input,p.options);}};
}
