/** Local source planning and reproducible archives; no CLI or network required. */
import { constants, createWriteStream } from 'node:fs';
import { lstat, realpath, opendir, open, mkdtemp, rename, rm } from 'node:fs/promises';
import type { Stats } from 'node:fs';
import { resolve, relative, isAbsolute, join, dirname, basename, sep } from 'node:path';
import { createHash } from 'node:crypto';
import { createGzip } from 'node:zlib';
import { Readable, Transform } from 'node:stream';
import { pipeline } from 'node:stream/promises';

export type SourcePreparationCode = 'invalid_input' | 'unsafe_path' | 'unsupported_file' | 'source_changed' | 'limit_exceeded' | 'secret_detected' | 'deadline_exceeded' | 'invalid_source';
export interface SourceIssue { readonly path: string; readonly code: SourcePreparationCode; readonly message: string; }
export interface SourceEntry {
  readonly path: string; readonly kind: 'file' | 'directory'; readonly size: number; readonly mode: number;
  readonly included: boolean; readonly reason: string; readonly ruleFile?: string; readonly ruleLine?: number;
  readonly symlink: boolean; readonly sha256?: string; readonly generated?: boolean;
}
export interface SourcePlan {
  readonly contextRoot: string; readonly sourceRoot: string; readonly dockerfile: boolean;
  readonly entries: readonly SourceEntry[]; readonly issues: readonly SourceIssue[];
  readonly fileCount: number; readonly totalBytes: number;
}
export interface SourcePreparationProgress { readonly phase: 'scanning' | 'packing'; readonly path: string; readonly bytes: number; }
export interface SourcePreparationOptions {
  /** Application root within directory. The archive retains the whole context for workspaces. */
  readonly sourceRoot?: string;
  /** Defaults to a Dockerfile marker at sourceRoot. Set explicitly for gregale.yaml build sections. */
  readonly dockerfile?: boolean;
  readonly exclude?: readonly string[];
  readonly secretScan?: 'strict' | 'off';
  readonly timeoutMs?: number;
  readonly signal?: AbortSignal;
  /** Includes directory headers, matching the server's entry limit. */
  readonly maxEntries?: number;
  readonly maxBytes?: number;
  readonly maxArchiveBytes?: number;
  readonly onProgress?: (progress: SourcePreparationProgress) => void | Promise<void>;
  readonly onPlan?: (plan: SourcePlan) => void | Promise<void>;
}
export interface PreparedSource { readonly path: string; readonly size: number; readonly sha256: string; readonly plan: SourcePlan; }
export class SourcePreparationError extends Error {
  constructor(readonly code: SourcePreparationCode, message: string, readonly plan?: SourcePlan) { super(message); this.name = 'SourcePreparationError'; }
}

// pkg/api/limits.go, not plan quotas. Callers can only lower these bounds.
const ENTRY_LIMIT = 10_000, BYTE_LIMIT = 2 * 1024 ** 3, CHUNK = 64 * 1024;
const excludedDirs = new Set(['.git','node_modules','vendor','__pycache__','dist','.next','coverage','target','.venv','.cache']);
const buildDirs = new Set(['dist','.next','coverage','target','.cache']);
const excludedFiles = new Set(['.git','.DS_Store','Thumbs.db']);
interface Rule { parts: string[]; anchor: boolean; dirOnly: boolean; negate: boolean; file: string; line: number; }
interface Snapshot { path: string; target: string; signature: string; }
interface Inspection { plan: SourcePlan; snapshots: Map<string, Snapshot>; files?:Readonly<Record<string,string>>; }
function fail(code: SourcePreparationCode, message: string): never { throw new SourcePreparationError(code,message); }
function inside(root: string, path: string): boolean { const rel = relative(root,path); return rel !== '..' && !rel.startsWith('..'+sep) && !isAbsolute(rel); }
function signature(s: Stats): string { return [s.dev,s.ino,s.size,s.mode,s.mtimeMs,s.ctimeMs].join(':'); }
function safeName(path: string): boolean { return !!path && !path.includes('\\') && !/[\x00-\x1f\x7f]/.test(path) && Buffer.from(path).toString('utf8') === path; }
function rootName(value: string): string {
  if (value === '.' || value === '') return '.';
  if (!safeName(value) || isAbsolute(value) || value.split('/').some(v=>!v || v === '.' || v === '..') || value.includes(':')) fail('invalid_input','sourceRoot must be a relative slash-separated directory');
  return value;
}
class Operation {
  readonly options: SourcePreparationOptions; readonly controller = new AbortController(); readonly deadline: number;
  private timer: ReturnType<typeof setTimeout>; private externalAbort: () => void;
  constructor(options: SourcePreparationOptions) {
    this.options = { ...options, exclude:[...(options.exclude ?? [])] };
    const timeout = options.timeoutMs ?? 120_000;
    if (!Number.isFinite(timeout) || timeout <= 0 || timeout > 2_147_483_647) fail('invalid_input','timeoutMs must be positive and at most 2147483647');
    for (const [name,value,max] of [['maxEntries',options.maxEntries,ENTRY_LIMIT],['maxBytes',options.maxBytes,BYTE_LIMIT],['maxArchiveBytes',options.maxArchiveBytes,Number.MAX_SAFE_INTEGER]] as const) {
      if (value !== undefined && (!Number.isSafeInteger(value) || value < 1 || value > max)) fail('invalid_input',`${name} must be a positive integer at most ${max}`);
    }
    if (options.secretScan !== undefined && !['strict','off'].includes(options.secretScan)) fail('invalid_input','secretScan must be strict or off');
    rootName(options.sourceRoot ?? '.');
    for (const path of this.options.exclude!) if (rootName(path) === '.') fail('invalid_input','exclude must name entries within the context');
    this.deadline = performance.now()+timeout;
    this.externalAbort = () => this.controller.abort(options.signal?.reason);
    options.signal?.addEventListener('abort',this.externalAbort,{once:true});
    if (options.signal?.aborted) this.externalAbort();
    this.timer = setTimeout(()=>this.controller.abort(new SourcePreparationError('deadline_exceeded','Source preparation deadline exceeded')),timeout);
  }
  check(): void {
    if (performance.now() >= this.deadline && !this.controller.signal.aborted) this.controller.abort(new SourcePreparationError('deadline_exceeded','Source preparation deadline exceeded'));
    this.controller.signal.throwIfAborted();
  }
  async callback<T>(callback: ((value:T)=>void|Promise<void>)|undefined,value:T): Promise<void> {
    this.check(); if (!callback) return;
    const signal = this.controller.signal;
    let listener!: () => void;
    try { await Promise.race([Promise.resolve().then(()=>{ this.check(); return callback(value); }), new Promise<never>((_,reject)=> { listener = ()=>reject(signal.reason); signal.addEventListener('abort',listener,{once:true}); if (signal.aborted) listener(); })]); }
    finally { signal.removeEventListener('abort',listener); }
    this.check();
  }
  close(): void { clearTimeout(this.timer); this.options.signal?.removeEventListener('abort',this.externalAbort); }
}
function rules(data: string,file: string): Rule[] {
  return data.split('\n').flatMap((raw,index)=> {
    let line=raw.trim(); if (!line || line.startsWith('#')) return [];
    const negate=line.startsWith('!'); if (negate) line=line.slice(1);
    let anchor=line.startsWith('/'); if (anchor) line=line.slice(1);
    const dirOnly=line.endsWith('/'); if (dirOnly) line=line.slice(0,-1);
    if (!line) return [];
    const parts=line.split('/');
    if (file === '.dockerignore') { anchor=true; while (parts.length>1 && parts[0] === '.') parts.shift(); }
    return [{parts,anchor,dirOnly,negate,file,line:index+1}];
  });
}
// Go filepath.Match grammar, including escaped characters and [^...] classes.
function segment(pattern: string,value: string): boolean {
  let regex='^';
  const quote=(v:string)=>v.replace(/[.*+?^${}()|[\]\\]/g,'\\$&');
  for (let i=0;i<pattern.length;i++) {
    const c=pattern[i]!;
    if (c === '*') regex+='.*';
    else if (c === '?') regex+='.';
    else if (c === '\\') { if (++i >= pattern.length) return false; regex+=quote(pattern[i]!); }
    else if (c === '[') {
      let cls='['; if (pattern[i+1] === '^') { cls+='^'; i++; }
      let count=0;
      for (i++;i<pattern.length && pattern[i] !== ']';i++) {
        let ch=pattern[i]!; if (ch === '\\') { if (++i>=pattern.length) return false; ch=pattern[i]!; cls+='\\'+ch; }
        else cls+=ch === '[' ? '\\[' : ch;
        count++;
      }
      if (!count || i>=pattern.length) return false;
      regex+=cls+']';
    } else regex+=quote(c);
  }
  try { return new RegExp(regex+'$','u').test(value); } catch { return false; }
}
function match(parts: string[],patterns: string[]): boolean {
  // Dynamic programming bounds ** matching by path/pattern length.
  const memo=new Map<string,boolean>();
  const visit=(i:number,j:number):boolean=> {
    const key=`${i}:${j}`; if (memo.has(key)) return memo.get(key)!;
    const p=patterns[j];
    const result=p === undefined ? i===parts.length : p==='**' ? (j===patterns.length-1 ? i<parts.length : visit(i,j+1) || i<parts.length && visit(i+1,j)) : i<parts.length && segment(p,parts[i]!) && visit(i+1,j+1);
    memo.set(key,result); return result;
  };
  return visit(0,0);
}
function selection(path: string,dir: boolean,docker: boolean,vendor: boolean,patterns: Rule[]): Pick<SourceEntry,'included'|'reason'|'ruleFile'|'ruleLine'> {
  const base=path.split('/').at(-1)!;
  if (base === '.git') return {included:false,reason:'VCS metadata is always excluded'};
  let included=true,reason=dir?'source directory':'source file';
  if (dir && docker && buildDirs.has(base)) reason='Dockerfile build output';
  else if (dir && base === 'vendor' && vendor) reason='Go module vendoring';
  else if (dir && excludedDirs.has(base)) { included=false; reason='default build-artifact exclusion'; }
  else if (!dir && (excludedFiles.has(base) || base.endsWith('.pyc'))) { included=false; reason='default generated/metadata exclusion'; }
  let result={included,reason} as Pick<SourceEntry,'included'|'reason'|'ruleFile'|'ruleLine'>;
  const parts=path.split('/');
  for (const rule of patterns) if ((!rule.dirOnly || dir) && (rule.anchor ? match(parts,rule.parts) : parts.some((_,i)=>match(parts.slice(i),rule.parts)))) result={included:rule.negate,reason:rule.negate?'negated ignore rule':'ignore rule',ruleFile:rule.file,ruleLine:rule.line};
  return result;
}
const providerPatterns: readonly [string,string,RegExp][] = [
  ['deploy','gregale_deploy_token',/fp_deploy_[0-9a-f]{48}/],['stripe','stripe_live',/sk_live_[A-Za-z0-9]{24,}/],['stripe','stripe_test',/sk_test_[A-Za-z0-9]{24,}/],
  ['github','github_pat',/gh[pousr]_[A-Za-z0-9]{30,}/],['aws','aws_access',/AKIA[0-9A-Z]{16}/],
  ['openai','openai',/sk-[A-Za-z0-9]{20,}T3BlbkFJ[A-Za-z0-9]{20,}/],['anthropic','anthropic',/sk-ant-[A-Za-z0-9-]{32,}/],
  ['google','google_api',/AIza[0-9A-Za-z_-]{35}/],['','private_key_block',/-----BEGIN (RSA |EC |DSA |OPENSSH |PGP )?PRIVATE KEY-----/],
];
function secretLine(line: string,inPem:boolean): string|undefined {
  const stripped=line.trim().replace(/^export /,''); if (stripped.startsWith('#')) return;
  const eq=stripped.indexOf('='); if (eq<0) return;
  const key=stripped.slice(0,eq).trim().toLowerCase(); let value=stripped.slice(eq+1);
  if (value.length>=2 && value[0]===value.at(-1) && ['"',"'"].includes(value[0]!)) value=value.slice(1,-1);
  const provider=providerPatterns.find(([hint,,pattern])=>key.includes(hint) && pattern.test(value))?.[1];
  if (provider) return provider;
  if (!inPem && value.length>=20 && !/^[a-z][a-z0-9+.-]+:/.test(value)) {
    const counts=new Map<string,number>(); for (const c of value) counts.set(c,(counts.get(c) ?? 0)+1);
    const entropy=-[...counts.values()].reduce((sum,n)=>sum+n/value.length*Math.log2(n/value.length),0);
    if (entropy>=4.5) return 'high_entropy';
  }
  return undefined;
}
async function snapshot(root:string,path:string):Promise<Snapshot> {
  const target=await realpath(path); if (!inside(root,target)) fail('unsafe_path','Path leaves source context');
  const info=await lstat(target); return {path,target,signature:signature(info)};
}
async function verify(root:string,s:Snapshot):Promise<void> {
  let actual:Snapshot;
  try { actual=await snapshot(root,s.path); } catch { fail('source_changed','Source path changed during preparation'); }
  if (actual.target!==s.target || actual.signature!==s.signature) fail('source_changed','Source changed during preparation');
}
async function* contents(root:string,s:Snapshot,op:Operation):AsyncGenerator<Buffer> {
  op.check(); await verify(root,s);
  const file=await open(s.target,constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0) | (constants.O_NONBLOCK ?? 0));
  try {
    if (signature(await file.stat())!==s.signature) fail('source_changed','Source changed before reading');
    let position=0;
    while (true) {
      op.check(); const buffer=Buffer.alloc(CHUNK); const {bytesRead}=await file.read(buffer,0,buffer.length,position);
      if (!bytesRead) break;
      position+=bytesRead; yield buffer.subarray(0,bytesRead);
    }
    if (signature(await file.stat())!==s.signature) fail('source_changed','Source changed while reading');
    await verify(root,s);
  } finally { await file.close(); }
}
async function inspect(directory:string,op:Operation,virtualPaths:readonly string[]=[]):Promise<Inspection> {
  op.check(); const root=await realpath(resolve(directory)),rootInfo=await lstat(root);
  if (!rootInfo.isDirectory()) fail('invalid_input','Source context must be a directory');
  const sourceRoot=rootName(op.options.sourceRoot ?? '.'), selected=await realpath(resolve(root,sourceRoot));
  if (!inside(root,selected) || !(await lstat(selected)).isDirectory()) fail('unsafe_path','sourceRoot must stay inside the context');
  const snapshots=new Map<string,Snapshot>(); snapshots.set('',await snapshot(root,root));
  let dockerfile=op.options.dockerfile ?? false;
  if (op.options.dockerfile===undefined) {
    let count=0;
    for await (const entry of await opendir(selected)) {
      op.check(); if (++count>ENTRY_LIMIT*4) fail('limit_exceeded','Application root exceeds inspection entry limit');
      if (!entry.isDirectory() && entry.name.toLowerCase()==='dockerfile') { dockerfile=true; break; }
    }
  }
  let patterns:Rule[]=[];
  for (const name of [...(dockerfile?['.dockerignore']:[]),'.gregaleignore']) {
    const path=join(root,name);
    try {
      const s=await snapshot(root,path); snapshots.set('\0'+name,s);
      const buffers:Buffer[]=[]; let size=0;
      for await (const data of contents(root,s,op)) { size+=data.length; if (size>1024*1024) fail('limit_exceeded','Ignore file exceeds 1 MiB'); buffers.push(data); }
      patterns.push(...rules(Buffer.concat(buffers).toString('utf8'),name));
    } catch(error) { if ((error as NodeJS.ErrnoException).code!=='ENOENT') throw error; }
  }
  for(const path of virtualPaths)if(op.options.exclude!.includes(path)||!selection(path,false,dockerfile,false,patterns).included)fail('invalid_source','Project transport file is excluded');
  const entries:SourceEntry[]=[],issues:SourceIssue[]=[]; let fileCount=0,totalBytes=0,visited=0,includedCount=0;
  const walk=async (physical:string,prefix:string,ancestors:Set<string>,depth:number,linkDepth=0):Promise<void>=> {
    op.check();
    for await (const child of await opendir(physical)) {
      const name=child.name;
      op.check(); const path=prefix?prefix+'/'+name:name,lexical=join(physical,name);
      if (++visited>(op.options.maxEntries ?? ENTRY_LIMIT)*4) fail('limit_exceeded','Source selection exceeds inspection entry limit');
      if (!safeName(path) || path.split('/').some(p=>p.includes(':'))) { issues.push(Object.freeze({path,code:'unsafe_path',message:'Unsupported archive path'})); continue; }
      let s:Snapshot,info:Stats,link:boolean;
      try { const raw=await lstat(lexical); link=raw.isSymbolicLink(); s=await snapshot(root,lexical); info=await lstat(s.target); }
      catch { issues.push(Object.freeze({path,code:'unsafe_path',message:'Path does not resolve inside the source context'})); continue; }
      const dir=info.isDirectory();
      let vendor=false;
      if (dir && name==='vendor') { try { vendor=(await lstat(join(root,path,'modules.txt'))).isFile(); } catch { /* absent */ } }
      let decision=selection(path,dir,dockerfile,vendor,patterns);
      if (op.options.exclude!.includes(path)) decision={included:false,reason:'command-scoped exclusion'};
      const entry:SourceEntry={path,kind:dir?'directory':'file',size:dir?0:info.size,mode:dir?0o755:(info.mode&0o111?0o755:0o644),symlink:link,...decision};
      if (!decision.included) { entries.push(Object.freeze(entry)); continue; }
      if (!dir && !info.isFile()) { issues.push(Object.freeze({path,code:'unsupported_file',message:'Device, socket or pipe cannot be packed'})); entries.push(Object.freeze(entry)); continue; }
      if (dir && (ancestors.has(s.target) || depth>=64 || link && linkDepth>=40)) { issues.push(Object.freeze({path,code:'unsafe_path',message:'Directory link cycle or excessive depth'})); continue; }
      snapshots.set(path,s);
      if (++includedCount>(op.options.maxEntries ?? ENTRY_LIMIT)) fail('limit_exceeded','Source has too many archive entries');
      if (dir) { entries.push(Object.freeze(entry)); await walk(s.target,path,new Set([...ancestors,s.target]),depth+1,linkDepth+(link?1:0)); continue; }
      totalBytes+=info.size; fileCount++;
      if (totalBytes>(op.options.maxBytes ?? BYTE_LIMIT)) fail('limit_exceeded','Source exceeds expanded byte limit');
      const hash=createHash('sha256'); let bytes=0,pending=''; const env=name==='env' || name==='.env' || name.startsWith('.env.'); let secret=false,inPem=false;
      const scan=(line:string)=> {
        if (line.includes('-----BEGIN ') && line.includes('PRIVATE KEY')) inPem=true;
        if (line.includes('-----END ') && line.includes('PRIVATE KEY')) inPem=false;
        secret=secret || secretLine(line,inPem)!==undefined;
      };
      for await (const data of contents(root,s,op)) {
        bytes+=data.length; if (bytes>info.size) fail('source_changed','Source file grew during inspection'); hash.update(data);
        if (op.options.secretScan!=='off' && env) {
          pending+=data.toString('latin1'); let end:number;
          while ((end=pending.indexOf('\n'))>=0) { if (end>CHUNK) fail('limit_exceeded','Dotenv line exceeds 64 KiB'); scan(pending.slice(0,end)); pending=pending.slice(end+1); }
          if (pending.length>CHUNK) fail('limit_exceeded','Dotenv line exceeds 64 KiB');
        }
        await op.callback(op.options.onProgress,Object.freeze({phase:'scanning',path,bytes}));
      }
      if (bytes!==info.size) fail('source_changed','Source file shrank during inspection');
      if (op.options.secretScan!=='off' && env) { scan(pending); if (secret) issues.push(Object.freeze({path,code:'secret_detected',message:'Credential-shaped dotenv value; move it to Gregale environment configuration or exclude the file'})); }
      entries.push(Object.freeze({...entry,sha256:hash.digest('hex')}));
    }
  };
  await walk(root,'',new Set([root]),0);
  if (!fileCount) issues.push(Object.freeze({path:'.',code:'invalid_source',message:'Source context contains no included regular files'}));
  if (sourceRoot!=='.' && !snapshots.has(sourceRoot)) issues.push(Object.freeze({path:sourceRoot,code:'invalid_source',message:'Application source root is excluded from the archive'}));
  for (const s of snapshots.values()) { op.check(); await verify(root,s); }
  entries.sort((a,b)=>Buffer.compare(Buffer.from(a.path),Buffer.from(b.path)));
  const plan=Object.freeze({contextRoot:root,sourceRoot,dockerfile,entries:Object.freeze(entries),issues:Object.freeze(issues),fileCount,totalBytes});
  return {plan,snapshots};
}
/** Read-only plan, including blocking issues. Does not create an archive. */
export async function inspectSource(directory:string,options:SourcePreparationOptions={}):Promise<SourcePlan> {
  const op=new Operation(options); try { return (await inspect(directory,op)).plan; } finally { op.close(); }
}
function tarHeader(name:string,size:number,mode:number,type:string):Buffer {
  const h=Buffer.alloc(512); h.write(name,0,100,'utf8');
  const oct=(offset:number,length:number,value:number)=>h.write(value.toString(8).padStart(length-1,'0')+'\0',offset,length,'ascii');
  oct(100,8,mode); oct(108,8,0); oct(116,8,0); oct(124,12,size); oct(136,12,0); h.fill(32,148,156); h.write(type,156,1); h.write('ustar\0',257,6); h.write('00',263,2);
  const sum=h.reduce((a,b)=>a+b,0); h.write(sum.toString(8).padStart(6,'0')+'\0 ',148,8); return h;
}
function paxPath(path:string):Buffer {
  const record=' path='+path+'\n'; let size=Buffer.byteLength(record)+1;
  while (String(size).length+Buffer.byteLength(record)!==size) size=String(size).length+Buffer.byteLength(record);
  return Buffer.from(size+record);
}
async function* tar(source:Inspection,op:Operation):AsyncGenerator<Buffer> {
  for (const entry of source.plan.entries.filter(e=>e.included)) {
    op.check(); let name=entry.path+(entry.kind==='directory'?'/':'');
    if (Buffer.byteLength(name)>100 || /[^\x20-\x7e]/.test(name)) {
      const pax=paxPath(name); yield tarHeader('PaxHeaders/source',pax.length,0o644,'x'); yield pax;
      if (pax.length%512) yield Buffer.alloc(512-pax.length%512);
      name='PaxEntry';
    }
    yield tarHeader(name,entry.size,entry.mode,entry.kind==='directory'?'5':'0');
    if (entry.kind==='directory') continue;
    const hash=createHash('sha256'); let bytes=0;
    const generated=source.files?.[entry.path];
    const dataChunks=async function*(){if(generated===undefined){yield* contents(source.plan.contextRoot,source.snapshots.get(entry.path)!,op);}else{const data=Buffer.from(generated);for(let offset=0;offset<data.length;offset+=CHUNK){op.check();yield data.subarray(offset,offset+CHUNK);}}};
    for await (const data of dataChunks()) {
      bytes+=data.length; if (bytes>entry.size) fail('source_changed','Source file grew during packing'); hash.update(data); yield data;
      await op.callback(op.options.onProgress,Object.freeze({phase:'packing',path:entry.path,bytes}));
    }
    if (bytes!==entry.size || hash.digest('hex')!==entry.sha256) fail('source_changed','Source content changed after inspection');
    if (entry.size%512) yield Buffer.alloc(512-entry.size%512);
  }
  yield Buffer.alloc(1024);
}
/** Atomically writes a 0600 archive outside directory; preserves an existing destination on failure. */
export async function prepareSource(directory:string,outputPath:string,options:SourcePreparationOptions={}):Promise<PreparedSource> {
  return prepareSourceWithFiles(directory,outputPath,options,{});
}
/** Internal project transport files. The customer tree is never rewritten. */
export async function prepareSourceWithFiles(directory:string,outputPath:string,options:SourcePreparationOptions,files:Readonly<Record<string,string>>):Promise<PreparedSource> {
  const op=new Operation(options); let temporary:string|undefined;
  try {
    const root=await realpath(resolve(directory)),parent=await realpath(dirname(resolve(outputPath))),output=join(parent,basename(outputPath));
    if (inside(root,parent)) fail('invalid_input','Archive destination must be outside the source context');
    const source=await inspect(root,op,Object.keys(files));
    if(Object.keys(files).length){
      const entries=[...source.plan.entries];
      for(const [path,data]of Object.entries(files)){
        rootName(path);if(!['go.mod','gregale.yaml'].includes(path.split('/').at(-1)!))fail('invalid_input','Unsupported project transport file');
        if(typeof data!=='string'||Buffer.byteLength(data)>1024*1024)fail('limit_exceeded','Project transport file exceeds 1 MiB');
        const previous=entries.find(e=>e.path===path);
        if(previous && (!previous.included||previous.kind!=='file'))fail('invalid_source','Project transport file is excluded or conflicts with a directory');
        const parents=path.split('/');parents.pop();while(parents.length){const p=entries.find(e=>e.path===parents.join('/'));if(!p?.included||p.kind!=='directory')fail('invalid_source','Project transport parent is excluded');parents.pop();}
        const entry:SourceEntry=Object.freeze({path,kind:'file',size:Buffer.byteLength(data),mode:0o644,included:true,reason:'project transport file',symlink:false,sha256:createHash('sha256').update(data).digest('hex'),generated:true});
        if(previous)entries.splice(entries.indexOf(previous),1,entry);else entries.push(entry);
      }
      entries.sort((a,b)=>Buffer.compare(Buffer.from(a.path),Buffer.from(b.path)));
      const included=entries.filter(e=>e.included),totalBytes=included.reduce((n,e)=>n+e.size,0);
      if(included.length>(op.options.maxEntries??ENTRY_LIMIT)||totalBytes>(op.options.maxBytes??BYTE_LIMIT))fail('limit_exceeded','Project transport files exceed source limits');
      source.plan=Object.freeze({...source.plan,entries:Object.freeze(entries),fileCount:included.filter(e=>e.kind==='file').length,totalBytes});source.files={...files};
    }
    await op.callback(op.options.onPlan,source.plan);
    if (source.plan.issues.length) throw new SourcePreparationError('invalid_source','Source plan has blocking issues',source.plan);
    temporary=await mkdtemp(join(parent,'.gregale-source-')); const path=join(temporary,'archive');
    let size=0; const hash=createHash('sha256');
    try {
      const meter=new Transform({transform(chunk:Buffer,_encoding,callback) { size+=chunk.length; if (size>(op.options.maxArchiveBytes ?? Number.MAX_SAFE_INTEGER)) { callback(new SourcePreparationError('limit_exceeded','Compressed archive exceeds byte limit')); return; } hash.update(chunk); callback(null,chunk); }});
      await pipeline(Readable.from(tar(source,op)),createGzip({level:6}),meter,createWriteStream(path,{flags:'wx',mode:0o600}),{signal:op.controller.signal});
      for (const s of source.snapshots.values()) { op.check(); await verify(root,s); }
      const file=await open(path,'r+'); try { await file.sync(); } finally { await file.close(); }
    } catch(error) { op.check(); throw error; }
    op.check(); await rename(path,output);
    return Object.freeze({path:output,size,sha256:hash.digest('hex'),plan:source.plan});
  } finally { op.close(); if (temporary) await rm(temporary,{recursive:true,force:true}); }
}
