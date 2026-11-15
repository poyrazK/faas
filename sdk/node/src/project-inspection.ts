/** Local project discovery. Never runs project code, shells, package managers or network requests. */
import { constants } from 'node:fs';
import { open, lstat, realpath, opendir } from 'node:fs/promises';
import { resolve, relative, dirname, isAbsolute, sep, posix } from 'node:path';
import { createHash } from 'node:crypto';
import { parseDocument } from 'yaml';
import { parse as parseToml } from 'smol-toml';
import type { UploadDeployOptions } from './generated/models/UploadDeployOptions.js';

export type ProjectShape = 'app' | 'function' | 'unknown';
export type ProjectFramework = 'docker' | 'node' | 'python' | 'go' | 'unknown';
export type ProjectIssueCode = 'invalid_manifest' | 'unsafe_path' | 'ambiguous_selection' | 'missing_input' | 'unsupported_settings' | 'unknown_project' | 'validation_deferred';
export interface ProjectIssue { readonly code:ProjectIssueCode; readonly severity:'error'|'warning'; readonly path:string; readonly message:string; }
export interface ProjectInput { readonly path:string; readonly kind:'file'|'directory'; readonly sha256:string|null; }
export interface ProjectSetting { readonly field:string; readonly value:string|boolean|null; readonly origin:'override'|'manifest'|'detection'|'default'; readonly file:string|null; readonly key:string|null; }
export interface ProjectCandidate { readonly name:string; readonly root:string; readonly kind:'workspace'|'function'; }
export interface ProjectGeneratedFile { readonly path:string; readonly size:number; readonly sha256:string; readonly purpose:'function_manifest'|'go_module'; }
export interface ProjectBuild { readonly dockerfile:string; readonly target:string|null; readonly argumentNames:readonly string[]; readonly secretIds:readonly string[]; }
export interface ProjectPlan {
  readonly version:1; readonly projectRoot:string; readonly contextRoot:string; readonly sourceRoot:string;
  readonly appSlug:string|null; readonly shape:ProjectShape; readonly framework:ProjectFramework;
  readonly runtime:string|null; readonly handler:string|null; readonly dockerfile:boolean;
  readonly functionName:string|null; readonly build:ProjectBuild|null;
  readonly candidates:readonly ProjectCandidate[]; readonly settings:readonly ProjectSetting[]; readonly issues:readonly ProjectIssue[];
  readonly inputs:readonly ProjectInput[]; readonly generatedFiles:readonly ProjectGeneratedFile[]; readonly fingerprint:string;
}
export interface ProjectInspectionOptions {
  /** Explicit context boundary. Otherwise inspect the nearest Git root, or directory itself. */
  readonly contextRoot?:string;
  readonly sourceRoot?:string;
  readonly functionName?:string;
  readonly appSlug?:string;
  readonly runtime?:string;
  readonly handler?:string;
  readonly dockerfile?:boolean;
  readonly timeoutMs?:number;
  readonly signal?:AbortSignal;
  readonly maxEntries?:number;
}
export class ProjectInspectionError extends Error {
  constructor(readonly code:'invalid_input'|'limit_exceeded'|'deadline_exceeded'|'project_changed'|'invalid_plan',message:string) { super(message);this.name='ProjectInspectionError'; }
}
const markers:[string,ProjectFramework][]=[['Dockerfile','docker'],['package.json','node'],['requirements.txt','python'],['pyproject.toml','python'],['Pipfile','python'],['setup.py','python'],['go.mod','go']];
const handlers:Record<string,string>={'handler.js':'node22','handler.ts':'node22','handler.py':'python312','handler.go':'go124'};
const skip=new Set(['.git','node_modules','vendor','__pycache__','dist','.next','coverage','target','.venv','.cache']);
const manifestKeys=new Set('schema_version profiling tracing hosting build dev function functions release lifecycle scaling retry_policy queue_bindings triggers event_triggers work_policies exclusive_operations operations operation_workflows async_routes routes companions main_depends_on extensions workflows databases buckets worker realtime labels project'.split(' '));
const sum=(value:string|Buffer)=>createHash('sha256').update(value).digest('hex');
const sorted=(values:Iterable<string>)=>[...values].sort((a,b)=>Buffer.compare(Buffer.from(a),Buffer.from(b)));
function stable(value:unknown):string {
  if(Array.isArray(value))return '['+value.map(stable).join(',')+']';
  if(value && typeof value==='object')return '{'+sorted(Object.keys(value)).map(k=>JSON.stringify(k)+':'+stable((value as Record<string,unknown>)[k])).join(',')+'}';
  return JSON.stringify(value);
}
function freeze<T>(value:T):T { if(value && typeof value==='object'){for(const child of Object.values(value))freeze(child);Object.freeze(value);}return value; }
function inside(root:string,path:string):boolean {const r=relative(root,path);return r!=='..'&&!r.startsWith('..'+sep)&&!isAbsolute(r);}
function sourceRoot(value:string):string {
  const raw=value.trim();if(!raw||raw==='.')return '.';
  if(Buffer.byteLength(raw)>256||raw.includes('\\')||raw.includes(':')||/[\x00-\x1f\x7f]/.test(raw)||raw.startsWith('/')||raw.split('/').some(p=>!p||p==='.'||p==='..'))throw new Error('Invalid source root');
  return raw;
}
function localPath(value:string):string {const p=posix.normalize(value.trim().replace(/^\.\//,''));if(!p||p==='.'||p==='..'||p.startsWith('../')||value.startsWith('/')||value.includes('\\')||/[\x00-\x1f\x7f]/.test(value))throw new Error('Path must stay inside the source root');return p;}
function object(value:unknown):Record<string,unknown> {if(!value||typeof value!=='object'||Array.isArray(value))throw new Error('Expected mapping');return value as Record<string,unknown>;}
function string(value:unknown,fallback=''):string {if(value===undefined||value===null)return fallback;if(typeof value!=='string')throw new Error('Expected string');return value;}
function keys(value:Record<string,unknown>,allowed:string[]) {if(Object.keys(value).some(k=>!allowed.includes(k)))throw new Error('Unknown configuration key');}
function bounded(value:unknown,active=new Set<object>(),depth=0,budget={n:0}):void {
  if(++budget.n>20000||depth>64)throw new ProjectInspectionError('limit_exceeded','Configuration exceeds inspection limits');
  if(value&&typeof value==='object'){if(active.has(value))throw new Error('Cyclic configuration');active.add(value);for(const child of Object.values(value))bounded(child,active,depth+1,budget);active.delete(value);}
}
function yaml(text:string):unknown {const doc=parseDocument(text,{version:'1.2',uniqueKeys:true,merge:true});if(doc.errors.length||doc.warnings.some(w=>w.code==='TAG_RESOLVE_FAILED'))throw new Error('Invalid YAML');const value=doc.toJS({maxAliasCount:100});bounded(value);return value;}
function decode(name:string,text:string):unknown {const value=name.endsWith('.json')?JSON.parse(text):name.endsWith('.toml')?parseToml(text):yaml(text);bounded(value);return value;}
class Reader {
  readonly inputs=new Map<string,ProjectInput>();readonly directories=new Map<string,string[]>();entries=0;
  readonly deadline:number;readonly options:ProjectInspectionOptions;
  constructor(readonly root:string,options:ProjectInspectionOptions){this.options={...options};const timeout=options.timeoutMs??120000;if(!Number.isFinite(timeout)||timeout<=0||timeout>2147483647||!Number.isSafeInteger(options.maxEntries??40000)||((options.maxEntries??40000)<1||(options.maxEntries??40000)>40000))throw new ProjectInspectionError('invalid_input','Invalid inspection limits');this.deadline=performance.now()+timeout;}
  check(){this.options.signal?.throwIfAborted();if(performance.now()>=this.deadline)throw new ProjectInspectionError('deadline_exceeded','Project inspection deadline exceeded');}
  async file(path:string):Promise<string|null>{
    this.check();const absolute=resolve(this.root,path);if(!inside(this.root,absolute))throw new Error('Path leaves project');
    let target:string;try{target=await realpath(absolute);}catch(e){if((e as NodeJS.ErrnoException).code==='ENOENT'){this.inputs.set(path,{path,kind:'file',sha256:null});return null;}throw e;}
    if(!inside(this.root,target))throw new Error('Path leaves project');
    const file=await open(target,constants.O_RDONLY|(constants.O_NOFOLLOW??0)|(constants.O_NONBLOCK??0));
    try{const before=await file.stat();if(!before.isFile()||before.size>1024*1024)throw new ProjectInspectionError('limit_exceeded','Inspection input must be a regular file at most 1 MiB');const chunks:Buffer[]=[];let size=0;while(size<=1024*1024){this.check();const buffer=Buffer.alloc(Math.min(65536,1024*1024+1-size));const {bytesRead}=await file.read(buffer);if(!bytesRead)break;chunks.push(buffer.subarray(0,bytesRead));size+=bytesRead;}const data=Buffer.concat(chunks);const after=await file.stat();if(data.length>1024*1024||before.ino!==after.ino||before.size!==after.size||before.mtimeMs!==after.mtimeMs||before.ctimeMs!==after.ctimeMs||await realpath(absolute)!==target)throw new ProjectInspectionError('project_changed','Inspection input changed');this.inputs.set(path,{path,kind:'file',sha256:sum(data)});this.check();return data.toString('utf8');}finally{await file.close();}
  }
  async names(path:string):Promise<string[]>{
    this.check();if(this.directories.has(path))return this.directories.get(path)!;
    const absolute=await realpath(resolve(this.root,path));if(!inside(this.root,absolute))throw new Error('Directory leaves project');
    const names:string[]=[],description:string[]=[];
    for await(const entry of await opendir(absolute)){this.check();if(++this.entries>(this.options.maxEntries??40000))throw new ProjectInspectionError('limit_exceeded','Project has too many inspection entries');names.push(entry.name);description.push(entry.name+':'+(entry.isDirectory()?'directory':entry.isSymbolicLink()?'symlink':'file'));}
    const result=sorted(names);this.directories.set(path,result);this.inputs.set(path,{path,kind:'directory',sha256:sum(stable(sorted(description)))});return result;
  }
  async regular(path:string):Promise<boolean>{try{const target=await realpath(resolve(this.root,path));if(!inside(this.root,target))throw new Error('Path leaves project');return(await lstat(target)).isFile();}catch(e){if((e as NodeJS.ErrnoException).code==='ENOENT')return false;throw e;}}
}
function workspacePattern(raw:string):{exclude:boolean;pattern:RegExp}|null{
  let p=raw.trim();const exclude=p.startsWith('!');if(exclude)p=p.slice(1).trim();p=p.replace(/^\.\//,'').replace(/\/+$/,'');
  if(!p||p.startsWith('/')||p.includes('\\')||/[\[\]{}()]/.test(p))throw new Error('Invalid workspace pattern');if(p.split('/').some(s=>!s||s==='.'||s==='..'))return null;
  let regex='^';for(let i=0;i<p.length;i++){const c=p[i]!;if(c==='*'&&p[i+1]==='*'){if(p[i+2]==='/'){regex+='(?:[^/]+/)*';i+=2;}else{regex+='.*';i++;}}else if(c==='*')regex+='[^/]*';else if(c==='?')regex+='[^/]';else regex+=c.replace(/[.*+?^${}()|[\]\\]/g,'\\$&');}return {exclude,pattern:new RegExp(regex+'$')};
}
async function workspaceMembers(r:Reader):Promise<string[]>{
  const patterns:string[][]=[],literal:string[]=[];let nx=false;
  for(const name of ['package.json','pnpm-workspace.yaml','turbo.json','nx.json','go.work','Cargo.toml']){
    const text=await r.file(name);if(text===null)continue;
    if(name==='go.work'){let block=false;for(let line of text.split('\n')){line=line.replace(/\/\/.*$/,'').trim();if(/^use\s*\(/.test(line)){block=true;line=line.replace(/^use\s*\(/,'').trim();}else if(line.startsWith('use '))line=line.slice(4).trim();else if(!block)continue;if(line===')'){block=false;continue;}if(line){if(line.startsWith('"'))line=JSON.parse(line);literal.push(line.replace(/^\.\//,''));}}continue;}
    const data=object(decode(name,text));
    if(name==='package.json'){const w=data.workspaces;if(w!==undefined)patterns.push((Array.isArray(w)?w:object(w).packages) as string[]);}
    if(name==='pnpm-workspace.yaml'&&data.packages!==undefined)patterns.push(data.packages as string[]);
    if(name==='turbo.json')for(const k of ['pipeline','$pipeline'])if(data[k])literal.push(...Object.keys(object(data[k])));
    if(name==='nx.json'){nx=true;if(data.projects)for(const [key,value]of Object.entries(object(data.projects)))literal.push(typeof value==='string'?value:typeof object(value).root==='string'?object(value).root as string:key);}
    if(name==='Cargo.toml'&&data.workspace){const w=object(data.workspace);patterns.push([...(w.members as string[]??[]),...(w.exclude as string[]??[]).map(s=>'!'+s)]);}
  }
  if(!patterns.length&&!literal.length&&!nx)return [];
  const dirs:string[]=[];
  const walk=async(path:string,depth:number):Promise<void>=>{if(depth>64)throw new ProjectInspectionError('limit_exceeded','Project nesting exceeds 64');for(const name of await r.names(path)){if(skip.has(name)||name.startsWith('.'))continue;const p=posix.join(path,name);const info=await lstat(resolve(r.root,p));if(info.isDirectory()){dirs.push(p);await walk(p,depth+1);}else if(nx&&(name==='project.json'||name==='package.json')){const text=await r.file(p);if(text){const d=object(decode(p,text)),config=name==='project.json'?d:d.nx;if(config&&typeof config==='object')literal.push(typeof object(config).root==='string'?object(config).root as string:posix.dirname(p));}}}};
  await walk('.',0);const selected=new Set<string>();
  for(const group of patterns){if(!Array.isArray(group)||group.some(p=>typeof p!=='string'))throw new Error('Workspace paths must be strings');const local=new Set<string>();for(const raw of group){const p=workspacePattern(raw);if(p)for(const dir of dirs)if(p.pattern.test(dir)){if(p.exclude)local.delete(dir);else local.add(dir);}}for(const p of local)selected.add(p);}
  for(const raw of literal){const p=raw.trim().replace(/^(\.\/)+/,'').replace(/\/+$/,'');if(p&&!p.startsWith('..')&&!p.split('/').some(v=>!v||v==='.')&&dirs.includes(p))selected.add(p);}
  const out:string[]=[];for(const member of sorted(selected))for(const marker of ['Dockerfile','dockerfile','package.json','pyproject.toml','go.mod','Cargo.toml','pom.xml'])if(await r.regular(posix.join(member,marker))){out.push(member);break;}return out;
}
function wire(plan:ProjectPlan):unknown {return {...plan,projectRoot:'.',contextRoot:relative(plan.projectRoot,plan.contextRoot).split(sep).join('/')||'.',fingerprint:undefined};}
function fingerprint(plan:ProjectPlan):string {const value=wire(plan) as Record<string,unknown>;delete value.fingerprint;return sum(stable(value));}
export function projectDeployOptions(plan:ProjectPlan):UploadDeployOptions {return {source_root:plan.sourceRoot,...(plan.shape==='function'?{runtime:plan.runtime!,handler:plan.handler!}:{}),...(plan.dockerfile?{dockerfile:true}:{})};}
export function projectBuildFiles(plan:ProjectPlan):Readonly<Record<string,string>>{
  const files:Record<string,string>={};for(const entry of plan.generatedFiles)files[entry.path]=entry.purpose==='go_module'?'module gregale-function\n\ngo 1.26.11\n':JSON.stringify({function:{runtime:plan.runtime,handler:plan.handler}})+'\n';return files;
}
export function parseProjectPlan(value:unknown):ProjectPlan {
  const p=value as ProjectPlan;
  try{if(!p||p.version!==1||!isAbsolute(p.projectRoot)||!isAbsolute(p.contextRoot)||!inside(p.projectRoot,p.contextRoot)||sourceRoot(p.sourceRoot)!==p.sourceRoot||!Array.isArray(p.inputs)||p.inputs.length>40000||!Array.isArray(p.generatedFiles)||p.generatedFiles.length>2||!Array.isArray(p.issues)||fingerprint(p)!==p.fingerprint)throw new Error();
    const fields='version projectRoot contextRoot sourceRoot appSlug shape framework runtime handler dockerfile functionName build candidates settings issues inputs generatedFiles fingerprint'.split(' ');
    if(Object.keys(p).length!==fields.length||Object.keys(p).some(k=>!fields.includes(k))||!['app','function','unknown'].includes(p.shape)||!['docker','node','python','go','unknown'].includes(p.framework)||typeof p.dockerfile!=='boolean'||![p.appSlug,p.runtime,p.handler,p.functionName].every(v=>v===null||typeof v==='string')||!Array.isArray(p.candidates)||!Array.isArray(p.settings))throw new Error();
    for(const i of p.inputs)if((i.path!=='.'&&sourceRoot(i.path)!==i.path)||!['file','directory'].includes(i.kind)||(i.sha256!==null&&!/^[a-f0-9]{64}$/.test(i.sha256))||i.kind==='directory'&&i.sha256===null)throw new Error();
    if(new Set(p.inputs.map(i=>i.path)).size!==p.inputs.length||new Set(p.generatedFiles.map(i=>i.path)).size!==p.generatedFiles.length)throw new Error();
    for(const i of p.settings)if(typeof i.field!=='string'||!['override','manifest','detection','default'].includes(i.origin)||![null,'string','boolean'].includes(i.value===null?null:typeof i.value)||![i.file,i.key].every(v=>v===null||typeof v==='string'))throw new Error();
    for(const i of p.issues)if(!['error','warning'].includes(i.severity)||!['invalid_manifest','unsafe_path','ambiguous_selection','missing_input','unsupported_settings','unknown_project','validation_deferred'].includes(i.code)||typeof i.path!=='string'||typeof i.message!=='string')throw new Error();
    for(const i of p.candidates)if(!['workspace','function'].includes(i.kind)||typeof i.name!=='string'||sourceRoot(i.root)!==i.root)throw new Error();
    for(const i of p.generatedFiles)if(!['function_manifest','go_module'].includes(i.purpose)||posix.dirname(i.path)!==(p.sourceRoot==='.'?'.':p.sourceRoot)||posix.basename(i.path)!==(i.purpose==='go_module'?'go.mod':'gregale.yaml'))throw new Error();
    if(p.build&&(localPath(p.build.dockerfile)!==p.build.dockerfile||p.build.target!==null&&typeof p.build.target!=='string'||!Array.isArray(p.build.argumentNames)||!Array.isArray(p.build.secretIds)||![...p.build.argumentNames,...p.build.secretIds].every(v=>typeof v==='string')))throw new Error();
    for(const [path,data]of Object.entries(projectBuildFiles(p))){if(localPath(path)!==path||!['go.mod','gregale.yaml'].includes(posix.basename(path)))throw new Error();const e=p.generatedFiles.find(e=>e.path===path)!;if(e.size!==Buffer.byteLength(data)||e.sha256!==sum(data))throw new Error();}
    return freeze(structuredClone(p));
  }catch{throw new ProjectInspectionError('invalid_plan','Invalid project plan');}
}
export async function validateProjectPlan(plan:ProjectPlan,options:ProjectInspectionOptions={}):Promise<void>{
  const p=parseProjectPlan(plan);if(p.issues.some(i=>i.severity==='error'))throw new ProjectInspectionError('invalid_plan','Project plan has blocking issues');
  const r=new Reader(p.projectRoot,options);for(const i of p.inputs){if(i.kind==='file')await r.file(i.path);else await r.names(i.path);if(r.inputs.get(i.path)?.sha256!==i.sha256)throw new ProjectInspectionError('project_changed','Project configuration changed after inspection');}
}
export async function inspectProject(directory:string,options:ProjectInspectionOptions={}):Promise<ProjectPlan>{
  options.signal?.throwIfAborted();const selected=await realpath(resolve(directory));let root=options.contextRoot?await realpath(resolve(options.contextRoot)):selected;
  if(!inside(root,selected))throw new ProjectInspectionError('invalid_input','Directory must stay inside the inspection boundary');
  if(!options.contextRoot){let cursor=selected;for(let depth=0;depth<64;depth++){try{await lstat(resolve(cursor,'.git'));root=cursor;break;}catch(e){if((e as NodeJS.ErrnoException).code!=='ENOENT')throw e;}const parent=dirname(cursor);if(parent===cursor)break;cursor=parent;}}
  const r=new Reader(root,options),issues:ProjectIssue[]=[],settings:ProjectSetting[]=[],candidates:ProjectCandidate[]=[];
  const issue=(code:ProjectIssueCode,path:string,message:string,severity:'error'|'warning'='error')=>issues.push({code,path,message,severity});
  const setting=(field:string,value:string|boolean|null,origin:ProjectSetting['origin'],file:string|null=null,key:string|null=null)=>settings.push({field,value,origin,file,key});
  let context=selected,source='.',runtime:string|null=null,handler:string|null=null,shape:ProjectShape='unknown',framework:ProjectFramework='unknown',docker=false,appSlug=options.appSlug??null,functionName:string|null=null,build:ProjectBuild|null=null;
  const generated:ProjectGeneratedFile[]=[];
  try{
    const members=await workspaceMembers(r);for(const member of members)candidates.push({name:posix.basename(member),root:member,kind:'workspace'});
    const rel=relative(root,selected).split(sep).join('/')||'.';
    if(options.sourceRoot!==undefined){source=sourceRoot(options.sourceRoot);context=root;}
    else if(members.includes(rel)){source=rel;context=root;}
    else if(selected===root&&members.length){issue('ambiguous_selection','.',"Choose sourceRoot explicitly for a workspace repository");context=root;}
    let appDir=resolve(context,source);if(!inside(root,await realpath(appDir)))throw new Error('Source root leaves project');
    let manifest:Record<string,unknown>={},manifestPath:string|null=null;
    const load=async(dir:string):Promise<{data:Record<string,unknown>;path:string|null}>=>{const prefix=relative(root,dir).split(sep).join('/');for(const name of ['gregale.yaml','gregale.yml']){const path=prefix?prefix+'/'+name:name,text=await r.file(path);if(text!==null){const d=object(yaml(text));if(Object.keys(d).some(k=>!manifestKeys.has(k)))throw new Error('Unknown manifest key');if(d.schema_version!==undefined&&d.schema_version!==0&&d.schema_version!==1)throw new Error('Unsupported schema version');return {data:d,path};}}const path=prefix?prefix+'/gregale.toml':'gregale.toml';if(await r.file(path)!==null)issue('unsupported_settings',path,'TOML deployment declarations require the CLI');return {data:{},path:null};};
    const loaded=await load(appDir);manifest=loaded.data;manifestPath=loaded.path;
    if(manifest.project){const p=object(manifest.project);keys(p,['name','app','service_policy','services']);if(appSlug===null&&p.app!==undefined){appSlug=string(p.app);setting('appSlug',appSlug,'manifest',manifestPath,'project.app');}}
    if(manifest.function&&manifest.functions)throw new Error('function and functions are mutually exclusive');
    let fn:Record<string,unknown>|null=null;
    if(manifest.functions!==undefined){if(!Array.isArray(manifest.functions)||!manifest.functions.length)throw new Error('functions must be a nonempty list');const names=new Set<string>();for(const raw of manifest.functions){const f=object(raw),name=string(f.name);if(!/^[a-z]([a-z0-9-]{0,28}[a-z0-9])?$/.test(name)||names.has(name))throw new Error('Invalid or duplicate function name');names.add(name);const h=string(f.handler),rootPath=string(f.root).trim();if(!rootPath&&!h)throw new Error('Function root or handler is required');const cleaned=rootPath?posix.normalize(rootPath):'.';if(cleaned==='.'&&!h||cleaned==='..'||cleaned.startsWith('../')||rootPath.startsWith('/')||rootPath.includes('\\'))throw new Error('Function root leaves project');candidates.push({name,root:posix.join(relative(root,appDir).split(sep).join('/'),cleaned),kind:'function'});if(options.functionName===name||manifest.functions.length===1&&options.functionName===undefined){fn=f;functionName=name;}}
      if(!fn)issue('ambiguous_selection',manifestPath??'.','Select functionName from the declared functions');
      if(fn){if(Object.keys(fn).some(k=>!['name','root','runtime','handler'].includes(k))||Object.keys(manifest).some(k=>!['schema_version','functions','project'].includes(k))||manifest.project&&Object.keys(object(manifest.project)).some(k=>!['name','app'].includes(k)))issue('unsupported_settings',manifestPath??'.','Function resource settings require separate CLI reconciliation');
        const next=await realpath(resolve(appDir,string(fn.root)||'.'));if(!inside(root,next))throw new Error('Function root leaves project');appDir=next;const relativeApp=relative(root,appDir).split(sep).join('/');if(members.includes(relativeApp)){context=root;source=relativeApp;}else{context=appDir;source='.';}
      }
    }else if(options.functionName!==undefined)issue('ambiguous_selection',manifestPath??'.','No matching functions declaration');
    const names=await r.names(relative(root,appDir).split(sep).join('/')||'.');
    for(const [name,kind]of markers){const marker=names.find(n=>n.toLowerCase()===name.toLowerCase());if(marker&&await r.regular(posix.join(relative(root,appDir).split(sep).join('/'),marker))){await r.file(posix.join(relative(root,appDir).split(sep).join('/'),marker));if(framework==='unknown')framework=kind;}}
    if(fn){shape='function';runtime=string(fn.runtime)||null;handler=string(fn.handler)||null;if(runtime&&!['node22','node24','python312','python313','go124','go124-alpine','binary'].includes(runtime))throw new Error('Unknown function runtime');if(handler){handler=localPath(handler);if(runtime?.startsWith('go'))throw new Error('Go functions require their own root');if(!await r.regular(posix.join(relative(root,appDir).split(sep).join('/'),handler)))issue('missing_input',handler,'Declared function handler is missing');runtime??=handler.endsWith('.py')?'python312':/\.(js|mjs)$/.test(handler)?'node22':null;}else if(runtime)handler='handler.handler';else{const child=await load(appDir);if(child.data.function){const f=object(child.data.function);keys(f,['runtime','handler']);runtime=string(f.runtime).trim();handler=string(f.handler).trim();if(!runtime||!handler)throw new Error('function runtime and handler are required');setting('runtime',runtime,'manifest',child.path,'function.runtime');setting('handler',handler,'manifest',child.path,'function.handler');}}if(runtime&&!settings.some(s=>s.field==='runtime'))setting('runtime',runtime,fn.runtime?'manifest':'detection',manifestPath,`functions.${functionName}.runtime`);if(handler&&!settings.some(s=>s.field==='handler'))setting('handler',handler,fn.handler?'manifest':'default',manifestPath,`functions.${functionName}.handler`);}
    else if(manifest.function){const f=object(manifest.function);keys(f,['runtime','handler']);runtime=string(f.runtime).trim();handler=string(f.handler).trim();if(!runtime||!handler)throw new Error('function runtime and handler are required');shape='function';setting('runtime',runtime,'manifest',manifestPath,'function.runtime');setting('handler',handler,'manifest',manifestPath,'function.handler');}
    else if(manifest.build||framework!=='unknown')shape='app';
    const conventional=names.filter(n=>Object.hasOwn(handlers,n.toLowerCase()));
    if(shape==='unknown'&&conventional.length===1)shape='function';
    if(shape==='unknown'&&conventional.length>1)issue('ambiguous_selection','.', 'Several conventional handler files are present');
    if(shape==='function'&&!runtime&&conventional.length===1){runtime=handlers[conventional[0]!.toLowerCase()]!;handler='handler.handler';setting('runtime',runtime,'detection',relative(root,resolve(appDir,conventional[0]!)).split(sep).join('/'));setting('handler',handler,'default');}
    if(options.runtime!==undefined){runtime=options.runtime;shape='function';setting('runtime',runtime,'override');}
    if(options.handler!==undefined){handler=options.handler;shape='function';setting('handler',handler,'override');}
    if(shape==='function'){if(!runtime||!handler)issue('missing_input','.', 'Set the function runtime and handler');framework=runtime?.startsWith('node')?'node':runtime?.startsWith('python')?'python':runtime?.startsWith('go')?'go':'unknown';if(handler&&/\.(js|mjs|ts|py|go)$/.test(handler)){const p=localPath(handler);if(!await r.regular(posix.join(relative(root,appDir).split(sep).join('/'),p)))issue('missing_input',p,'Declared handler is missing');}}
    if(manifest.build){if(shape==='function')throw new Error('build and function settings are incompatible');const b=object(manifest.build);keys(b,['dockerfile','target','args','secrets']);const file=localPath(string(b.dockerfile,'Dockerfile')||'Dockerfile');const args=b.args===undefined?{}:object(b.args),secrets=b.secrets===undefined?[]:b.secrets;if(!Array.isArray(secrets))throw new Error('Invalid build secrets');const ids=secrets.map(s=>{const v=object(s);keys(v,['id','secret']);if(!string(v.id)||!string(v.secret))throw new Error('Invalid build secret reference');return string(v.id);});build={dockerfile:file,target:string(b.target)||null,argumentNames:sorted(Object.keys(args)),secretIds:sorted(ids)};docker=true;framework='docker';setting('dockerfile',true,'manifest',manifestPath,'build');const filePath=posix.join(relative(root,appDir).split(sep).join('/'),file);if(!await r.regular(filePath))issue('missing_input',file,'Selected Dockerfile is missing');else await r.file(filePath);}
    else docker=framework==='docker';
    if(options.dockerfile!==undefined){docker=options.dockerfile;setting('dockerfile',docker,'override');if(docker){shape='app';framework='docker';if(!await r.regular(posix.join(relative(root,appDir).split(sep).join('/'),'Dockerfile')))issue('missing_input','Dockerfile','Dockerfile override requires Dockerfile');}}
    if(options.appSlug!==undefined)setting('appSlug',options.appSlug,'override');if(appSlug!==null&&!/^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/.test(appSlug))throw new Error('Invalid app slug');
    if(shape==='unknown')issue('unknown_project','.', 'No supported build marker or function handler was found');
    setting('sourceRoot',source,options.sourceRoot!==undefined?'override':source!=='.'?'detection':'default');
    if(!settings.some(s=>s.field==='dockerfile'))setting('dockerfile',docker,'detection');
    for(const key of Object.keys(manifest))if(!['schema_version','function','functions','build','project'].includes(key))issue('validation_deferred',manifestPath??'.','Section '+key+' requires server or CLI validation','warning');
    const files:Record<string,{data:string;purpose:ProjectGeneratedFile['purpose']}>={};
    if(fn&&runtime&&handler)files[posix.join(source,'gregale.yaml')]={data:JSON.stringify({function:{runtime,handler}})+'\n',purpose:'function_manifest'};
    if(shape==='function'&&runtime?.startsWith('go')&&!await r.regular(posix.join(relative(root,appDir).split(sep).join('/'),'go.mod')))files[posix.join(source,'go.mod')]={data:'module gregale-function\n\ngo 1.26.11\n',purpose:'go_module'};
    for(const path of sorted(Object.keys(files))){const f=files[path]!;generated.push({path,size:Buffer.byteLength(f.data),sha256:sum(f.data),purpose:f.purpose});}
  }catch(error){if(error instanceof ProjectInspectionError||options.signal?.aborted)throw error;issue((error as Error).message.includes('leave')?'unsafe_path':'invalid_manifest','.', 'Project configuration is invalid; check selected paths and manifest fields');}
  const plan={version:1 as const,projectRoot:root,contextRoot:context,sourceRoot:source,appSlug,shape,framework,runtime,handler,dockerfile:docker,functionName,build,candidates,settings,issues,inputs:sorted(r.inputs.keys()).map(k=>r.inputs.get(k)!),generatedFiles:generated,fingerprint:''};
  plan.fingerprint=fingerprint(plan);return freeze(plan);
}
