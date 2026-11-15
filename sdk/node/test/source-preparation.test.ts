import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, mkdir, writeFile, readFile, rm, symlink, chmod, stat, utimes, readdir } from 'node:fs/promises';
import { readFileSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { tmpdir } from 'node:os';
import { gunzipSync } from 'node:zlib';
import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { inspectSource, prepareSource, SourcePreparationError, type SourcePreparationOptions } from '../src/index.js';
import * as preview from '../src/preview.js';

interface Case { name:string; files:Record<string,string>; links?:Record<string,string>; executable?:string[]; source_root?:string; exclude?:string[]; included:string[]; }
const fixture=JSON.parse(readFileSync(new URL('../../../request-contracts/source-preparation.json',import.meta.url),'utf8')) as {
  cases:Case[]; secrets:{name:string;line_parts:string[];blocked:boolean}[];
};
async function create(root:string,item:Case) {
  for (const [path,data] of Object.entries(item.files)) { await mkdir(dirname(join(root,path)),{recursive:true}); await writeFile(join(root,path),data); }
  for (const [path,target] of Object.entries(item.links ?? {})) await symlink(target,join(root,path));
  for (const path of item.executable ?? []) await chmod(join(root,path),0o755);
}
function archive(data:Buffer) {
  const bytes=gunzipSync(data),entries:{path:string;data:Buffer;mode:number}[]=[]; let pax:string|undefined;
  for (let i=0;i<bytes.length;) {
    const h=bytes.subarray(i,i+512); if (h.every(v=>v===0)) break;
    const text=(start:number,end:number)=>h.subarray(start,end).toString('utf8').replace(/\0.*$/s,'');
    const size=parseInt(text(124,136),8),type=text(156,157),body=bytes.subarray(i+512,i+512+size);
    assert.equal(parseInt(text(136,148),8),0); assert.equal(parseInt(text(108,116),8),0); assert.equal(parseInt(text(116,124),8),0);
    const checksum=h.reduce((sum,value,index)=>sum+(index>=148 && index<156?32:value),0); assert.equal(parseInt(text(148,156),8),checksum);
    if (type==='x') pax=body.toString('utf8').replace(/^\d+ path=/,'').replace(/\n$/,'');
    else { entries.push({path:(pax ?? text(0,100)).replace(/\/$/,''),data:Buffer.from(body),mode:parseInt(text(100,108),8)}); pax=undefined; }
    i+=512+Math.ceil(size/512)*512;
  }
  return entries;
}
const code=(value:string)=>(error:unknown)=>error instanceof SourcePreparationError && error.code===value;

for (const [surface,sdk] of [['public',{inspectSource,prepareSource}],['preview',preview]] as const) {
  for (const item of fixture.cases) test(`${surface} source contract: ${item.name}`,async t=> {
    const home=await mkdtemp(join(tmpdir(),'gregale-preparation-')); t.after(()=>rm(home,{recursive:true,force:true}));
    const root=join(home,'source'); await mkdir(root); await create(root,item);
    const options:SourcePreparationOptions={sourceRoot:item.source_root,exclude:item.exclude};
    const plan=await sdk.inspectSource(root,options);
    assert.ok(Object.isFrozen(plan)); assert.ok(Object.isFrozen(plan.entries)); assert.deepEqual(plan.issues,[]);
    assert.deepEqual(plan.entries.filter(e=>e.included && e.kind==='file').map(e=>e.path).sort(),[...item.included].sort());
    const first=await sdk.prepareSource(root,join(home,'first.tgz'),options);
    // Tar/gzip bytes do not depend on source mtime or destination spelling.
    for (const path of Object.keys(item.files)) await utimes(join(root,path),1,1);
    const second=await sdk.prepareSource(root,join(home,'second.tgz'),options);
    const bytes=await readFile(first.path); assert.deepEqual(bytes,await readFile(second.path));
    assert.equal(first.sha256,createHash('sha256').update(bytes).digest('hex')); assert.equal(first.size,bytes.length);
    assert.equal((await stat(first.path)).mode&0o777,0o600);
    const actual=archive(bytes);
    assert.deepEqual(actual.filter(e=>plan.entries.some(p=>p.path===e.path && p.kind==='file')).map(e=>e.path).sort(),[...item.included].sort());
    for (const entry of actual) { const planned=plan.entries.find(e=>e.path===entry.path)!; assert.equal(entry.mode,planned.mode); if (planned.kind==='file') assert.deepEqual(entry.data,await readFile(join(root,entry.path))); }
    // Also prove compatibility with a system tar reader independent of our test decoder.
    const listing=execFileSync('tar',['-tzf',first.path],{encoding:'utf8'}); for (const path of item.included) assert.ok(listing.includes(path));
  });
}
for (const item of fixture.secrets) test(`strict dotenv contract: ${item.name}`,async t=> {
  const home=await mkdtemp(join(tmpdir(),'gregale-preparation-secret-')); t.after(()=>rm(home,{recursive:true,force:true}));
  const line=item.line_parts.join('');
  const root=join(home,'src'); await mkdir(root); await writeFile(join(root,'.env'),line+'\n');
  const plan=await inspectSource(root); assert.equal(plan.issues.some(e=>e.code==='secret_detected'),item.blocked);
  assert.ok(!JSON.stringify(plan).includes(line));
  if (item.blocked) { await assert.rejects(prepareSource(root,join(home,'out.tgz')),error=>code('invalid_source')(error) && !!(error as SourcePreparationError).plan); assert.deepEqual((await inspectSource(root,{secretScan:'off'})).issues,[]); }
});
test('unsafe links, cycles and irregular files are inspectable blocking issues',async t=> {
  const home=await mkdtemp(join(tmpdir(),'gregale-preparation-unsafe-')); t.after(()=>rm(home,{recursive:true,force:true}));
  const root=join(home,'src'); await mkdir(root); await writeFile(join(root,'main.js'),'source'); await writeFile(join(home,'private'),'private');
  await symlink('../private',join(root,'escape')); await symlink('absent',join(root,'dangling')); await symlink('.',join(root,'cycle'));
  execFileSync('mkfifo',[join(root,'pipe')]);
  const plan=await inspectSource(root); assert.equal(plan.issues.length,4); assert.ok(plan.issues.some(e=>e.code==='unsupported_file'));
  await assert.rejects(prepareSource(root,join(home,'out.tgz')),code('invalid_source'));
});
for (const change of ['content','directory','ignore','symlink'] as const) test(`source ${change} changes preserve the destination`,async t=> {
  const home=await mkdtemp(join(tmpdir(),'gregale-preparation-change-')); t.after(()=>rm(home,{recursive:true,force:true}));
  const root=join(home,'src'),output=join(home,'out.tgz'); await mkdir(root); await writeFile(join(root,'file'),'before'); await writeFile(output,'existing');
  await assert.rejects(prepareSource(root,output,{onPlan:async()=> {
    if (change==='content') await writeFile(join(root,'file'),'after!');
    if (change==='directory') await writeFile(join(root,'added'),'new');
    if (change==='ignore') await writeFile(join(root,'.gregaleignore'),'file\n');
    if (change==='symlink') { await rm(join(root,'file')); await symlink('../out.tgz',join(root,'file')); }
  }}),code('source_changed'));
  assert.equal(await readFile(output,'utf8'),'existing'); assert.deepEqual((await readdir(home)).sort(),['out.tgz','src']);
});
test('budgets, deadlines, abort and slow callbacks clean up without publication',async t=> {
  const home=await mkdtemp(join(tmpdir(),'gregale-preparation-bounds-')); t.after(()=>rm(home,{recursive:true,force:true}));
  const root=join(home,'src'),output=join(home,'out.tgz'); await mkdir(root); await writeFile(join(root,'one'),'12345'); await writeFile(join(root,'two'),'12345'); await writeFile(output,'existing');
  for (const options of [{maxBytes:1},{maxEntries:1},{maxArchiveBytes:1}]) await assert.rejects(prepareSource(root,output,options),code('limit_exceeded'));
  await assert.rejects(prepareSource(root,join(root,'out.tgz')),code('invalid_input'));
  await assert.rejects(inspectSource(root,{sourceRoot:'../outside'}),code('invalid_input'));
  await assert.rejects(prepareSource(root,output,{timeoutMs:20,onPlan:()=>new Promise(()=>{})}),code('deadline_exceeded'));
  const controller=new AbortController(),reason=new Error('caller stopped');
  await assert.rejects(prepareSource(root,output,{signal:controller.signal,onProgress:p=> { if(p.phase==='packing') controller.abort(reason); }}),error=>error===reason);
  assert.equal(await readFile(output,'utf8'),'existing'); assert.deepEqual((await readdir(home)).sort(),['out.tgz','src']);
});
test('independent concurrent preparations stream bounded chunks',async t=> {
  const home=await mkdtemp(join(tmpdir(),'gregale-preparation-concurrent-')); t.after(()=>rm(home,{recursive:true,force:true}));
  const roots=[join(home,'a'),join(home,'b')]; for (const [i,root] of roots.entries()) { await mkdir(root); await writeFile(join(root,'file'),Buffer.alloc(3*1024*1024,i+1)); }
  let largest=0; const previous=new Map<string,number>();
  const results=await Promise.all(roots.map((root,i)=>prepareSource(root,join(home,`${i}.tgz`),{onProgress:p=> { const key=`${i}:${p.phase}:${p.path}`,last=previous.get(key)??0; largest=Math.max(largest,p.bytes-last); previous.set(key,p.bytes); }})));
  assert.equal(largest,65536); assert.notEqual(results[0]!.sha256,results[1]!.sha256);
});
test('empty plans and excluded application roots are blocking issues',async t=> {
  const home=await mkdtemp(join(tmpdir(),'gregale-preparation-empty-')); t.after(()=>rm(home,{recursive:true,force:true}));
  const root=join(home,'src'); await mkdir(root);
  assert.ok((await inspectSource(root)).issues.some(e=>e.code==='invalid_source'));
  await mkdir(join(root,'apps')); await writeFile(join(root,'apps','main.js'),'source'); await writeFile(join(root,'other.js'),'source');
  const plan=await inspectSource(root,{sourceRoot:'apps',exclude:['apps']}); assert.ok(plan.issues.some(e=>e.path==='apps'));
});
test('mutation while streaming cannot publish an archive',async t=> {
  const home=await mkdtemp(join(tmpdir(),'gregale-preparation-live-')); t.after(()=>rm(home,{recursive:true,force:true}));
  const root=join(home,'src'),output=join(home,'out.tgz'); await mkdir(root); await writeFile(join(root,'file'),Buffer.alloc(256*1024,1)); await writeFile(output,'existing');
  let changed=false;
  await assert.rejects(prepareSource(root,output,{onProgress:async p=> { if(p.phase==='packing' && !changed) { changed=true; await writeFile(join(root,'file'),Buffer.alloc(256*1024,2)); } }}),code('source_changed'));
  assert.equal(await readFile(output,'utf8'),'existing');
});
