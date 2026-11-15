import test from 'node:test';
import { once } from 'node:events';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { mkdtemp, writeFile, readFile, rm, unlink } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createClient, SourceUploadError, saveSourceUploadCheckpoint, loadSourceUploadCheckpoint,
  parseSourceUploadCheckpoint, type SourceUploadCheckpoint, type SourceUploadOptions, type UploadDeployOptions } from '../src/index.js';
import { createClient as createPreviewClient } from '../src/preview.js';

interface Case { name: string; scenario: string; outcome: string | null; }
const fixture = JSON.parse(readFileSync(new URL('../../../request-contracts/source-uploads.json', import.meta.url), 'utf8')) as {
  version: number; source_text: string; chunk_size: number; metadata: UploadDeployOptions; metadata_sha256: string;
  app_id: string; deployment_id: string; cases: Case[];
};
for (const [surface, factory] of [['public',createClient],['preview',createPreviewClient]] as const) for (const item of fixture.cases) {
  test(`${surface} shared source uploads: ${item.name}`, { timeout: 8000 }, async t => {
    const scenario = item.scenario, root = await mkdtemp(join(tmpdir(),'gregale-source-contract-'));
    t.after(() => rm(root,{ recursive:true,force:true }));
    const path = join(root,'source.tar.gz'), checkpointPath = join(root,'checkpoint.json');
    const source = Buffer.from(fixture.source_text); await writeFile(path, source);
    let received = Buffer.alloc(0), state = 'open', commits = 0, deployments = 0, opened = 0;
    let saved = '';
    const load = async () => scenario.endsWith('timeout') ? parseSourceUploadCheckpoint(JSON.parse(saved)) : loadSourceUploadCheckpoint(checkpointPath);
    let current: SourceUploadCheckpoint | undefined, receipt: Record<string,string> | undefined;
    let enter!: () => void; const entered = new Promise<void>(resolve => { enter = resolve; });
    const calls: string[] = [], keys: string[] = [], offsets: number[] = [], once = new Set<string>();
    const controller = new AbortController(), reason = new Error('caller stopped');
    const session = () => ({ upload_id:'session-1', app_slug:'api', chunk_size:fixture.chunk_size, total_size:source.length,
      received_bytes:received.length+(scenario === 'bad_offset' ? 1 : 0), status:state, expires_at:'2026-11-16T00:00:00Z',
      deployment_id: state === 'committed' ? fixture.deployment_id : null });
    const client = factory('https://uploads.test/prefix', { token:'token-before',timeoutMs:1,fetch:async (url, init) => {
      const headers = new Headers(init?.headers), method = init!.method!, route = new URL(String(url)).pathname.replace('/prefix','');
      assert.equal(headers.get('Authorization'),'Bearer token-before'); calls.push(`${method} ${route}`);
      if (method === 'POST' && route === '/v1/uploads') {
        keys.push(headers.get('Idempotency-Key')!);
        const body = JSON.parse(init!.body as string) as Record<string,unknown>;
        assert.deepEqual(body.deploy_options, fixture.metadata); assert.equal(body.app_slug,'api'); assert.equal(body.total_size,source.length);
        opened = 1;
        if (scenario === 'lost_start' && !once.has(scenario)) { once.add(scenario); throw new TypeError('reply lost'); }
        return Response.json({ upload_id:'session-1',chunk_size:fixture.chunk_size,total_size:source.length,expires_at:'2026-11-16T00:00:00Z' },{status:201});
      }
      if (method === 'GET' && route === '/v1/uploads/session-1') return Response.json(session());
      if (method === 'PATCH') {
        assert.equal(headers.get('Content-Type'),'application/offset+octet-stream');
        const offset = Number(headers.get('Upload-Offset')), body = init!.body;
        assert.ok(body instanceof Blob || ArrayBuffer.isView(body));
        const data = body instanceof Blob ? Buffer.from(await body.arrayBuffer()) : Buffer.from(body as Uint8Array);
        offsets.push(offset); assert.equal(offset,received.length); assert.ok(data.length <= fixture.chunk_size);
        assert.deepEqual(data,source.subarray(offset,offset+data.length));
        if (['patch_timeout','patch_cancel'].includes(scenario)) { enter(); return new Promise<Response>(() => {}); }
        if (scenario === 'expired') { state = 'expired'; return Response.json({code:'upload_session_expired'},{status:410}); }
        if (['lost_before','conflict','recovery_limit'].includes(scenario) && !once.has(scenario)) {
          once.add(scenario); if (scenario === 'conflict') return Response.json({code:'upload_session_offset_conflict'},{status:409}); throw new TypeError('reply lost');
        }
        if (scenario === 'partial' && !once.has(scenario)) { once.add(scenario); received = Buffer.concat([received,data.subarray(0,2)]); throw new TypeError('reply lost'); }
        received = Buffer.concat([received,data]);
        if (scenario === 'lost_chunk' && !once.has(scenario)) { once.add(scenario); throw new TypeError('reply lost'); }
        return new Response(undefined,{status:200,headers:{'Upload-Offset':scenario === 'bad_ack' ? '4.0' : String(received.length)}});
      }
      if (method === 'GET' && route.includes('/deployment-submissions/')) {
        assert.ok(current); assert.ok(route.endsWith('/'+current.attempt_id));
        if (!receipt) return Response.json({code:'not_found'},{status:404});
        return Response.json({...receipt,...(scenario === 'bad_receipt' ? {attempt_id:'wrong-attempt'} : {})});
      }
      if (method === 'POST' && route.endsWith('/commit')) {
        commits++; assert.ok(current); assert.equal(headers.get('Deployment-Attempt-ID'),current.attempt_id);
        assert.equal(headers.get('Idempotency-Key'),current.commit_key); assert.equal(current.stage,'committing');
        assert.equal((await load()).stage,'committing');
        if (scenario === 'unknown_commit') throw new TypeError('reply lost');
        if (scenario === 'pending_commit') return Response.json({code:'deployment_attempt_pending'},{status:409});
        deployments++; state = 'committed';
        receipt = { attempt_id:current.attempt_id,app_id:fixture.app_id,deployment_id:fixture.deployment_id,accepted_at:'2026-11-14T14:45:00Z' };
        if (['commit_timeout','commit_cancel'].includes(scenario)) { enter(); return new Promise<Response>(() => {}); }
        if (scenario === 'lost_commit') throw new TypeError('reply lost');
        return Response.json({id:fixture.deployment_id},{status:201});
      }
      throw new Error(`Unexpected request ${method} ${route}`);
    } });
    const save = async (value:SourceUploadCheckpoint) => {
      current = value; assert.ok(Object.isFrozen(value)); assert.equal(value.options_sha256,fixture.metadata_sha256);
      assert.ok(!JSON.stringify(value).includes('token-before'));
      if (scenario === 'save_fail') throw new Error('storage unavailable');
      saved = JSON.stringify(value);
      if (!scenario.endsWith('timeout')) await saveSourceUploadCheckpoint(checkpointPath,value);
      if (scenario === 'changed_live' && value.stage === 'new') await writeFile(path,Buffer.alloc(source.length,120));
      if (['restart','changed_restart','wrong_base','wrong_metadata'].includes(scenario) && value.received_bytes === 4 && !once.has(scenario)) {
        once.add(scenario); throw new Error('process stopped after checkpoint');
      }
    };
    const options: SourceUploadOptions = { timeoutMs:scenario === 'patch_timeout' ? 500 : 3000,onCheckpoint:save,
      maxRecoveries:scenario === 'recovery_limit' ? 0 : 3, signal:controller.signal };
    const metadata = structuredClone(fixture.metadata);
    const upload = () => client.uploads.uploadSource({path,appSlug:'api',deployOptions:metadata},options);
    const hasCode = (code:string) => (error:unknown) => error instanceof SourceUploadError && error.code === code;
    if (['restart','changed_restart','wrong_base','wrong_metadata'].includes(scenario)) {
      await assert.rejects(upload(),hasCode('checkpoint_failed')); current = await load();
      if (scenario === 'changed_restart') await writeFile(path,Buffer.alloc(source.length,120));
      if (scenario === 'wrong_base') client.setBaseURL('https://other.test/prefix');
      if (scenario === 'wrong_metadata') metadata.runtime = 'node';
      const before = calls.length;
      const resume = client.uploads.uploadSource({path,appSlug:'api',deployOptions:metadata,checkpoint:current},options);
      if (item.outcome) { await assert.rejects(resume,hasCode(item.outcome)); assert.equal(calls.length,before); return; }
      current = await resume;
    } else if (['patch_timeout','patch_cancel'].includes(scenario)) {
      const pending = upload(), rejected = assert.rejects(pending,scenario === 'patch_cancel' ? error => error === reason : hasCode('timeout'));
      await entered; if (scenario === 'patch_cancel') controller.abort(reason); await rejected;
      assert.equal(commits,0); assert.equal(current!.pending_end,4); return;
    } else if (item.outcome && !['unknown_commit','pending_commit','commit_timeout','commit_cancel','bad_receipt'].includes(scenario)) {
      await assert.rejects(upload(),hasCode(item.outcome)); assert.equal(commits,0);
      if (scenario === 'save_fail') assert.equal(calls.length,0); return;
    } else current = await upload();
    assert.deepEqual(received,source); assert.equal(current.stage,'ready'); assert.equal(commits,0);
    let result;
    if (['commit_timeout','commit_cancel'].includes(scenario)) {
      const pending = client.uploads.commitSource({checkpoint:current},{...options,timeoutMs:scenario === 'commit_timeout' ? 500 : 3000});
      const rejected = assert.rejects(pending,scenario === 'commit_cancel' ? error => error === reason : hasCode('timeout'));
      await entered; if (scenario === 'commit_cancel') controller.abort(reason); await rejected;
      current = await load();
      result = await client.uploads.commitSource({checkpoint:current},{...options,signal:undefined});
    } else if (item.outcome) {
      await assert.rejects(client.uploads.commitSource({checkpoint:current},options),hasCode(item.outcome));
      current = await load(); const before = commits;
      await assert.rejects(client.uploads.commitSource({checkpoint:current},options)); assert.equal(commits,before); assert.equal(commits,1); return;
    } else result = await client.uploads.commitSource({checkpoint:current},options);
    assert.equal(result.deployment_id,fixture.deployment_id); assert.equal(commits,1); assert.equal(deployments,1); assert.equal(current.stage,'accepted');
    await unlink(path); current = await load();
    await client.uploads.commitSource({checkpoint:current},{...options,signal:undefined});
    assert.equal(commits,1); assert.equal(opened,1); assert.equal(new Set(keys).size,1);
    if (scenario === 'lost_start') assert.equal(keys.length,2);
    if (scenario === 'lost_chunk') assert.deepEqual(offsets,[0,4,8]);
    if (scenario === 'partial') assert.deepEqual(offsets,[0,2,6]);
    assert.ok((scenario.endsWith('timeout') ? saved : await readFile(checkpointPath,'utf8')).includes('accepted'));
  });
}

test('source configuration and caller parameters are captured before filesystem awaits', async t => {
  const root = await mkdtemp(join(tmpdir(),'gregale-source-snapshot-')); t.after(() => rm(root,{recursive:true,force:true}));
  const path = join(root,'source'); await writeFile(path,'archive');
  const seen: string[] = [];
  const client = createClient('https://before.test/prefix',{token:'before', fetch:async (url,init) => {
    assert.equal(new Headers(init?.headers).get('Authorization'),'Bearer before'); seen.push(String(url));
    if (init!.method === 'POST') { const body = JSON.parse(init!.body as string); assert.equal(body.app_slug,'api'); assert.equal(body.deploy_options.runtime,'python'); return Response.json({upload_id:'snapshot',total_size:7,chunk_size:16,expires_at:'2026-11-16T00:00:00Z'}); }
    if (init!.method === 'GET') return Response.json({upload_id:'snapshot',app_slug:'api',total_size:7,chunk_size:16,received_bytes:0,status:'open',expires_at:'2026-11-16T00:00:00Z'});
    return new Response(undefined,{headers:{'Upload-Offset':'7'}});
  }});
  const input = {path,appSlug:'api',deployOptions:{runtime:'python'}};
  const pending = client.uploads.uploadSource(input,{timeoutMs:2000,onCheckpoint:() => {}});
  input.path = 'does-not-exist'; input.appSlug = 'other'; input.deployOptions.runtime = 'node'; client.setToken('after'); client.setBaseURL('https://after.test');
  assert.equal((await pending).stage,'ready'); assert.ok(seen.every(url => url.startsWith('https://before.test/prefix/')));
});

test('source checkpoint deadlines prevent later writes after slow callbacks', async t => {
  const root = await mkdtemp(join(tmpdir(),'gregale-source-callback-')); t.after(() => rm(root,{recursive:true,force:true}));
  const path = join(root,'source'); await writeFile(path,'archive'); let calls = 0, release!: () => void, entered!: () => void;
  const started = new Promise<void>(resolve => { entered = resolve; }), gate = new Promise<void>(resolve => { release = resolve; });
  const client = createClient('https://never.test',{fetch:async () => { calls++; return Response.json({}); }});
  const pending = client.uploads.uploadSource({path,appSlug:'api'},{timeoutMs:100,onCheckpoint:async () => { entered(); await gate; }});
  const rejected = assert.rejects(pending,error => error instanceof SourceUploadError && error.code === 'timeout');
  await started; await rejected; release(); await new Promise(resolve => setTimeout(resolve,10)); assert.equal(calls,0);
});

test('source uploads bound memory and detect mutation of later file grains', async t => {
  const root = await mkdtemp(join(tmpdir(),'gregale-source-grains-')); t.after(() => rm(root,{recursive:true,force:true}));
  const path = join(root,'source'); const bytes = Buffer.alloc(17*1024*1024+7,97); await writeFile(path,bytes);
  const sizes: number[] = []; let offset = 0;
  const client = createClient('https://grains.test',{fetch:async (_url,init) => {
    if (init!.method === 'POST') return Response.json({upload_id:'grains',total_size:bytes.length,chunk_size:16*1024*1024,expires_at:'2026-11-16T00:00:00Z'});
    if (init!.method === 'GET') return Response.json({upload_id:'grains',app_slug:'api',total_size:bytes.length,chunk_size:16*1024*1024,received_bytes:offset,status:'open',expires_at:'2026-11-16T00:00:00Z'});
    const body = init!.body; assert.ok(body instanceof Blob); const length = body.size; sizes.push(length); offset += length;
    return new Response(undefined,{headers:{'Upload-Offset':String(offset)}});
  }});
  await client.uploads.uploadSource({path,appSlug:'api'},{timeoutMs:3000,onCheckpoint:async value => {
    if (value.received_bytes === 16*1024*1024) { const changed = Buffer.from(bytes); changed[16*1024*1024] = 98; await writeFile(path,changed); }
  }}).then(() => assert.fail('changed grain must stop'), error => assert.ok(error instanceof SourceUploadError && error.code === 'source_changed'));
  assert.deepEqual(sizes,[16*1024*1024]);
});

test('real HTTP concurrent uploads isolate accounts and reject cross-account checkpoints', { timeout:8000 }, async t => {
  const { createServer } = await import('node:http');
  const root = await mkdtemp(join(tmpdir(),'gregale-source-http-')); t.after(() => rm(root,{recursive:true,force:true}));
  const path = join(root,'source'); await writeFile(path,'abcdefghi');
  const sessions = new Map<string,{token:string;received:Buffer;total:number}>(); const seen: string[] = [];
  const server = createServer(async (request,response) => {
    try {
      const token = request.headers.authorization!.replace('Bearer ',''), parts:Buffer[] = []; for await (const part of request) parts.push(Buffer.from(part));
      const bytes = Buffer.concat(parts); seen.push(`${token} ${request.method} ${request.url}`); response.setHeader('Content-Type','application/json');
      if (request.method === 'POST') {
        const body = JSON.parse(bytes.toString()), id = token+'-'+sessions.size; sessions.set(id,{token,received:Buffer.alloc(0),total:body.total_size});
        response.writeHead(201); response.end(JSON.stringify({upload_id:id,total_size:body.total_size,chunk_size:4,expires_at:'2026-11-16T00:00:00Z'})); return;
      }
      const id = request.url!.split('/').at(-1)!, session = sessions.get(id)!;
      if (session.token !== token) { response.writeHead(403); response.end(JSON.stringify({code:'forbidden',status:403})); return; }
      if (request.method === 'GET') { response.end(JSON.stringify({upload_id:id,app_slug:'api',total_size:session.total,chunk_size:4,received_bytes:session.received.length,status:'open',expires_at:'2026-11-16T00:00:00Z'})); return; }
      assert.equal(Number(request.headers['upload-offset']),session.received.length); session.received = Buffer.concat([session.received,bytes]);
      response.setHeader('Upload-Offset',String(session.received.length)); response.end();
    } catch (error) { response.writeHead(500); response.end(JSON.stringify({code:'server_error'})); }
  });
  server.listen(0,'127.0.0.1'); await once(server,'listening'); t.after(() => { server.closeAllConnections(); server.close(); });
  const base = `http://127.0.0.1:${(server.address() as {port:number}).port}`;
  const alice = createClient(base,{token:'alice'}), bob = createClient(base,{token:'bob'});
  const values = await Promise.all(Array.from({length:6},(_,i) => (i%2 ? bob : alice).uploads.uploadSource({path,appSlug:'api'},{timeoutMs:4000,onCheckpoint:() => {}})));
  assert.equal(sessions.size,6); assert.ok([...sessions.values()].every(value => value.received.toString() === 'abcdefghi'));
  const before = seen.length;
  await assert.rejects(bob.uploads.uploadSource({path,appSlug:'api',checkpoint:values[0]},{timeoutMs:2000,onCheckpoint:() => {}}),error => error instanceof SourceUploadError && error.code === 'request_failed');
  assert.deepEqual(seen.slice(before),[`bob GET /v1/uploads/${values[0]!.upload_id}`]);
});
