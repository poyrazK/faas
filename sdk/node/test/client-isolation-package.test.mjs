import test from 'node:test';
import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { mkdtemp, mkdir, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const exec = promisify(execFile);
const sdk = fileURLToPath(new URL('..', import.meta.url));

// Packing and installing thousands of generated modules can exceed a minute on a busy filesystem.
test('packed SDK exposes isolated clients, pagination, waiters and the legacy migration API', { timeout: 120_000 }, async t => {
  const root = await mkdtemp(join(tmpdir(), 'gregale-client-isolation-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  // npm's JSON output lists thousands of generated files and can exceed the
  // child-process buffer. Silent output contains just the tarball filename.
  const { stdout } = await exec('npm', ['pack', '--silent', '--ignore-scripts', '--pack-destination', root], { cwd: sdk });
  const filename = stdout.trim();
  assert.match(filename, /^[\w.-]+\.tgz$/);
  const app = join(root, 'existing-app');
  await mkdir(app);
  await writeFile(join(app, 'package.json'), JSON.stringify({ private: true, type: 'module' }));
  await exec('npm', ['install', '--prefer-offline', '--ignore-scripts', '--no-audit', '--no-fund', '--omit=dev', join(root, filename)], { cwd: app });
  await writeFile(join(app, 'app.mjs'), `
    import assert from 'node:assert/strict';
    import { createClient, FaaSClient, AppsService, DeploymentsService, OpenAPI, PaginationError, WaiterError } from '@gregale/sdk-node';
    import { createClient as createPreviewClient } from '@gregale/sdk-node/preview';
    const sourceSdk = await import('@gregale/sdk-node');
    const { writeFile } = await import('node:fs/promises');
    await writeFile('source.tar.gz','archive');
    let sourceOffset = 0, sourceReceipt;
    const sourceClient = createClient('https://source.test',{fetch:async (url,init) => {
      const path = new URL(String(url)).pathname, headers = new Headers(init.headers);
      if (init.method === 'POST' && path === '/v1/uploads') return Response.json({upload_id:'source',total_size:7,chunk_size:4,expires_at:'2026-11-16T00:00:00Z'},{status:201});
      if (init.method === 'GET' && path === '/v1/uploads/source') return Response.json({upload_id:'source',app_slug:'api',total_size:7,chunk_size:4,received_bytes:sourceOffset,status:'open',expires_at:'2026-11-16T00:00:00Z'});
      if (init.method === 'PATCH') { sourceOffset += init.body.size; return new Response(undefined,{headers:{'Upload-Offset':String(sourceOffset)}}); }
      if (init.method === 'POST') { sourceReceipt = {attempt_id:headers.get('Deployment-Attempt-ID'),app_id:'3f6e0f10-c939-4e10-8e6b-15c1a39807ad',deployment_id:'4f6e0f10-c939-4e10-8e6b-15c1a39807ad',accepted_at:'2026-11-14T14:45:00Z'}; return Response.json({id:sourceReceipt.deployment_id},{status:201}); }
      return Response.json(sourceReceipt ?? {code:'not_found'},{status:sourceReceipt ? 200 : 404});
    }});
    const { mkdir:mkdirSource } = await import('node:fs/promises');
    await mkdirSource('source-project'); await writeFile('source-project/index.js','export default 1;');
    const prepared = await sourceSdk.prepareSource('source-project','prepared.tgz');
    assert.equal(prepared.plan.fileCount,1); assert.ok(prepared.size>0); assert.match(prepared.sha256,/^[a-f0-9]{64}$/);
    assert.equal((await sourceSdk.inspectSource('source-project')).fileCount,1);
    const previewSdk = await import('@gregale/sdk-node/preview'); assert.equal(previewSdk.prepareSource,sourceSdk.prepareSource);
    const sourceOptions = {timeoutMs:10000,onCheckpoint:value => sourceSdk.saveSourceUploadCheckpoint('source-state.json',value)};
    const staged = await sourceClient.uploads.uploadSource({path:'source.tar.gz',appSlug:'api'},sourceOptions);
    assert.equal(staged.stage,'ready');
    const sourceAccepted = await sourceClient.uploads.commitSource({checkpoint:await sourceSdk.loadSourceUploadCheckpoint('source-state.json')},sourceOptions);
    assert.equal(sourceAccepted.deployment_id,'4f6e0f10-c939-4e10-8e6b-15c1a39807ad');
    let flowSize=0,flowOffset=0,flowCommits=0,flowReceipt;
    const workflowClient=createClient('https://deployment.test',{fetch:async(url,init)=>{
      const path=new URL(String(url)).pathname,headers=new Headers(init.headers);
      if(init.method==='POST' && path==='/v1/uploads'){flowSize=JSON.parse(init.body).total_size;return Response.json({upload_id:'workflow',total_size:flowSize,chunk_size:4096,expires_at:'2026-11-20T00:00:00Z'},{status:201});}
      if(init.method==='GET' && path==='/v1/uploads/workflow')return Response.json({upload_id:'workflow',app_slug:'api',total_size:flowSize,chunk_size:4096,received_bytes:flowOffset,status:'open',expires_at:'2026-11-20T00:00:00Z'});
      if(init.method==='PATCH'){flowOffset+=init.body.size;return new Response(undefined,{headers:{'Upload-Offset':String(flowOffset)}});}
      if(init.method==='POST'){flowCommits++;flowReceipt={attempt_id:headers.get('Deployment-Attempt-ID'),app_id:'3f6e0f10-c939-4e10-8e6b-15c1a39807ad',deployment_id:'4f6e0f10-c939-4e10-8e6b-15c1a39807ad',accepted_at:'2026-11-14T17:00:00Z'};return Response.json({id:flowReceipt.deployment_id},{status:201});}
      if(path.endsWith('/timeline'))return Response.json({deployment_id:flowReceipt.deployment_id,app:'api',status:'live',phase:'released',summary:'done',traffic_percent:100,events:[],created_at:'2026-11-14T17:00:00Z'});
      return Response.json(flowReceipt ?? {code:'not_found'},{status:flowReceipt?200:404});
    }});
    const flowOptions={statePath:'deployment-state.json',timeoutMs:10000};
    await writeFile('source-project/package.json','{}');
    const project=await sourceSdk.inspectProject('source-project',{appSlug:'api'});
    assert.deepEqual(project.issues,[]);assert.ok(Object.isFrozen(project));
    const restored=sourceSdk.parseProjectPlan(JSON.parse(JSON.stringify(project)));
    const deployed=await workflowClient.deployments.deployProject(restored,flowOptions);
    assert.equal(deployed.outcome,'succeeded');assert.equal(deployed.timeline.phase,'released');
    assert.equal((await workflowClient.deployments.resumeProjectDeployment(project,flowOptions)).outcome,'succeeded');assert.equal(flowCommits,1);
    assert.equal((await sourceSdk.loadSourceDeploymentState(flowOptions.statePath)).upload.stage,'accepted');
    let projectPosts=0, projectReceipt;
    const projectClient=createClient('https://project.test',{fetch:async(url,init)=>{
      const path=new URL(String(url)).pathname;
      if(path.endsWith('/scan'))return Response.json({project_slug:'shop',scan_source:'single',tier:'single',workloads:[{name:'api',root_dir:'.',command:[],ports:[8080]}],managed:[],crons:[],observed_apps:0,observed_crons:0,limit_apps:25,limit_crons:25,can_apply:true,plan_token:'reviewed'});
      if(path.endsWith('/apply-plan')){projectPosts++;const attempt=new Headers(init.headers).get('Deployment-Attempt-ID');const apply={project_slug:'shop',scan_source:'single',can_apply:true,plan_token:'reviewed',submission_attempt_id:attempt,apps:[{slug:'api',id:'3f6e0f10-c939-4e10-8e6b-15c1a39807ad'}],builds:[{slug:'api',app_id:'3f6e0f10-c939-4e10-8e6b-15c1a39807ad',deployment_id:'4f6e0f10-c939-4e10-8e6b-15c1a39807ad',build_id:'5f6e0f10-c939-4e10-8e6b-15c1a39807ad'}]};projectReceipt={attempt_id:attempt,project_slug:'shop',selection_complete:true,status:'complete',apply};return Response.json(apply);}
      if(path.endsWith('/timeline'))return Response.json({deployment_id:'4f6e0f10-c939-4e10-8e6b-15c1a39807ad',app:'api',status:'live',phase:'released',summary:'test',traffic_percent:100,events:[],created_at:'2026-11-14T19:00:00Z'});
      return Response.json(projectReceipt);
    }});
    const reviewed=await projectClient.projects.previewLocal({directory:'source-project',projectSlug:'shop'},{timeoutMs:10000});
    const parsedReview=sourceSdk.parseReviewedProjectPlan(JSON.parse(JSON.stringify(reviewed))),projectOptions={statePath:'project-apply.json',timeoutMs:10000};
    assert.equal((await projectClient.projects.applyPlan(parsedReview,projectOptions)).outcome,'succeeded');
    assert.equal((await projectClient.projects.resumePlanApplication(parsedReview,projectOptions)).outcome,'succeeded');assert.equal(projectPosts,1);
    assert.equal((await sourceSdk.loadProjectApplicationState(projectOptions.statePath)).stage,'complete');
    const original = globalThis.fetch;
    const seen = [];
    const transport = async (url, init) => {
      seen.push([String(url), new Headers(init.headers).get('Authorization')]);
      return Response.json({ id: String(url) });
    };
    const a = createClient('https://a.test', { token: 'a', fetch: transport });
    const b = createClient('https://b.test', { token: 'b', fetch: transport });
    await Promise.all([a.apps.getApp({ slug: 'a' }), b.apps.getApp({ slug: 'b' })]);
    assert.deepEqual(seen, [['https://a.test/v1/apps/a', 'Bearer a'], ['https://b.test/v1/apps/b', 'Bearer b']]);
    assert.equal(globalThis.fetch, original);
    assert.equal('flags' in a, false);
    assert.equal(typeof createPreviewClient('https://preview.test').flags, 'object');
    const diagnostics = [];
    const observed = createClient('https://options.test', { onResponse: info => diagnostics.push(info), fetch: async () =>
      Response.json({ id: 'observed' }, { headers: { 'X-Request-ID': '11111111-1111-4111-8111-111111111111' } }) });
    const own = [];
    assert.equal((await observed.apps.getApp({ slug: 'observed' }, { timeoutMs: 1000, retry: false, onResponse: info => own.push(info) })).id, 'observed');
    await observed.account.getAccount(undefined, { timeoutMs: 0 });
    assert.equal(diagnostics.length, 2);
    assert.equal(own[0], diagnostics[0]);
    assert.ok(Object.isFrozen(own[0]));
    assert.equal(own[0].requestId, '11111111-1111-4111-8111-111111111111');
    const expired = createClient('https://deadline.test', { fetch: async () => new Promise(() => {}) });
    await assert.rejects(expired.apps.getApp({ slug: 'deadline' }, { timeoutMs: 10 }), { name: 'TimeoutError' });
    let pages = 0;
    const paged = createClient('https://pagination.test', { fetch: async url => {
      pages++;
      const params = new URL(String(url)).searchParams;
      const start = Number(params.get('before') ?? 0), size = Number(params.get('limit'));
      return Response.json({ items: Array.from({ length: size }, (_, i) => ({ id: String(start + i) })), next_before: String(start + size) });
    } });
    const iterator = paged.deployments.iterateBuilds({ limit: 2 }, { maxItems: 3 });
    assert.equal(pages, 0);
    const rows = [];
    for await (const row of iterator) rows.push(row.id);
    assert.deepEqual(rows, ['0', '1', '2']); assert.equal(pages, 2);
    assert.equal(typeof DeploymentsService.iterateBuilds, 'undefined');
    const invalid = createPreviewClient('https://cycle.test', { fetch: async () => Response.json({ items: [], next_before: 'repeat' }) });
    await assert.rejects(invalid.deployments.iterateBuildPages({ before: 'repeat' }).next(), error => error instanceof PaginationError && error.code === 'repeated_cursor');
    let polls = 0;
    const progress = [];
    const waits = createClient('https://waits.test', { fetch: async (url, init) => {
      assert.equal(init.method, 'GET');
      if (String(url).includes('/builds/')) return Response.json({ id: 'build', status: ++polls === 1 ? 'queued' : 'succeeded' });
      return Response.json({ deployment_id: 'deployment', status: 'live', phase: 'held', traffic_percent: 10, events: [] });
    } });
    assert.equal((await waits.deployments.waitForBuild({ id: 'build' }, { timeoutMs: 1000, minDelayMs: 1, maxDelayMs: 1, onProgress: value => { progress.push(value.status); } })).status, 'succeeded');
    assert.deepEqual(progress, ['queued', 'succeeded']);
    await assert.rejects(waits.deployments.waitForDeployment({ id: 'deployment' }, { timeoutMs: 1000 }), error => error instanceof WaiterError && error.code === 'blocked' && error.lastObserved.phase === 'held');
    assert.equal(typeof DeploymentsService.waitForBuild, 'undefined');
    const config = { ...OpenAPI };
    const legacy = new FaaSClient('https://legacy.test', { token: 'legacy', fetch: transport });
    try {
      await Promise.all([AppsService.getApp({ slug: 'old' }), a.apps.getApp({ slug: 'after' })]);
      assert.ok(seen.some(([url, token]) => url === 'https://legacy.test/v1/apps/old' && token === 'Bearer legacy'));
      assert.ok(seen.some(([url, token]) => url === 'https://a.test/v1/apps/after' && token === 'Bearer a'));
      let submissions = 0;
      let streams = 0;
      const events = [];
      const runs = createPreviewClient('https://runs.test/prefix', { token: 'runs-token', fetch: async (url, init) => {
        assert.ok(String(url).startsWith('https://runs.test/prefix/v1/executions'));
        assert.equal(new Headers(init.headers).get('Authorization'), 'Bearer runs-token');
        if (init.method === 'POST') { submissions++; return Response.json({ id: 'run/1', status: 'queued' }); }
        if (String(url).includes('/events')) {
          streams++;
          if (streams > 1) assert.match(String(url), /after=1/);
          return new Response(streams === 1
            ? 'id: 1\\nevent: stdout\\ndata: {"chunk":"hello"}\\n\\n'
            : 'id: 2\\nevent: terminal\\ndata: {"status":"succeeded"}\\n\\n',
            { headers: { 'Content-Type': 'text/event-stream' } });
        }
        return Response.json({ id: 'run/1', status: 'succeeded' });
      } });
      const receipt = await runs.runExecution({ runtime: 'node22', source: 'console.log(1)' }, {
        retryInitialMs: 0, retryMaxMs: 0, onEvent: event => events.push(event.type),
      });
      assert.equal(receipt.status, 'succeeded');
      assert.deepEqual(events, ['stdout', 'terminal']);
      assert.equal(submissions, 1);
      assert.equal(streams, 2);
      assert.equal('runExecution' in a, false);
    } finally { legacy.uninstall(); globalThis.fetch = original; Object.assign(OpenAPI, config); }
  `);
  await exec(process.execPath, ['app.mjs'], { cwd: app });
  await writeFile(join(app, 'app.ts'), `
    import { createClient, type GregaleClient, type AppResponse, type CancelablePromise, type RequestOptions, type ResponseInfo, type ResponseObserver, type PaginationOptions, type DeploymentPagination, type DeploymentResponse, type DeploymentListResponse, type BuildResponse, type BuildListResponse, PaginationError, FaaSClient, AppsService, DeploymentsService } from '@gregale/sdk-node';
    import { createClient as createPreviewClient, type GregaleClient as PreviewClient, type ExecutionResponse, type ExecutionEvent, type ExecutionHelpers, type RequestOptions as PreviewOptions } from '@gregale/sdk-node/preview';
    import { type BuildWaitOptions, type DeploymentWaitOptions, type DeploymentWaiters, type ReleaseTimeline, type WaiterSnapshot, type WaiterProgress, WaiterError } from '@gregale/sdk-node';
    const client: GregaleClient = createClient('https://api.test');
    const request: CancelablePromise<AppResponse> = client.apps.getApp({ slug: 'typed' });
    request.cancel();
    const observer: ResponseObserver = (info: ResponseInfo) => { const id: string | null = info.requestId; void id; };
    const options: RequestOptions = { signal: new AbortController().signal, timeoutMs: 1000, retry: false, onResponse: observer };
    const scoped: CancelablePromise<AppResponse> = client.apps.getApp({ slug: 'typed' }, options);
    scoped.cancel();
    void client.account.getAccount(undefined, options);
    void client.apps.listApps(undefined, options);
    const preview: PreviewClient = createPreviewClient('https://api.test');
    void preview.flags;
    const previewOptions: PreviewOptions = options;
    void preview.runs.getExecution({ id: 'run' }, previewOptions);
    const pagination: DeploymentPagination = client.deployments;
    const pageOptions: PaginationOptions = { ...options, maxItems: 5, maxPages: 2 };
    const deployments: AsyncGenerator<DeploymentResponse> = pagination.iterateAppDeployments({ slug: 'app' }, pageOptions);
    const deploymentPages: AsyncGenerator<DeploymentListResponse> = pagination.iterateAppDeploymentPages({ slug: 'app' });
    const account: AsyncGenerator<DeploymentResponse> = pagination.iterateDeployments();
    const accountPages: AsyncGenerator<DeploymentListResponse> = pagination.iterateDeploymentPages({}, pageOptions);
    const builds: AsyncGenerator<BuildResponse> = pagination.iterateBuilds({ status: 'running' }, pageOptions);
    const buildPages: AsyncGenerator<BuildListResponse> = preview.deployments.iterateBuildPages();
    const originalList: CancelablePromise<DeploymentListResponse> = client.deployments.listAppDeployments({ slug: 'app' });
    const invalidPage: Error = new PaginationError('invalid_page');
    void deployments; void deploymentPages; void account; void accountPages; void builds; void buildPages; void originalList; void invalidPage;
    // @ts-expect-error app pagination still requires an app slug
    pagination.iterateAppDeployments({});
    // @ts-expect-error generated build filters remain checked
    pagination.iterateBuilds({ status: 'unknown' });
    // @ts-expect-error limits are numeric
    pagination.iterateDeployments({}, { maxPages: '2' });
    // @ts-expect-error legacy static services retain their original surface
    DeploymentsService.iterateBuilds();
    const sourcePreparation = await import('@gregale/sdk-node');
    const preparedLocal:Promise<import('@gregale/sdk-node').PreparedSource> = sourcePreparation.prepareSource('project','archive.tgz',{
      sourceRoot:'apps/api',onPlan:plan => {
        const count:number=plan.fileCount; void count;
        // @ts-expect-error source plans are immutable
        plan.totalBytes=0;
      }});
    void preparedLocal;
    type WorkflowResult = import('@gregale/sdk-node').SourceDeploymentResult;
    const workflow:Promise<WorkflowResult> = client.deployments.deploySource({directory:'project',appSlug:'api'}, {statePath:'../state.json',timeoutMs:1000,onProgress:event=>{
      const phase:import('@gregale/sdk-node').SourceDeploymentPhase=event.phase;void phase;
      // @ts-expect-error progress is immutable
      event.elapsedMs=0;
    }});
    const resumed:Promise<WorkflowResult> = preview.deployments.resumeSourceDeployment({appSlug:'api'},{statePath:'../state.json',timeoutMs:1000});
    void workflow;void resumed;
    type Project = import('@gregale/sdk-node').ProjectPlan;
    const project:Project=await import('@gregale/sdk-node').then(s=>s.inspectProject('project',{appSlug:'api',sourceRoot:'apps/api'}));
    const projectRun:Promise<WorkflowResult>=client.deployments.deployProject(project,{statePath:'../project.json',timeoutMs:1000});
    const projectResume:Promise<WorkflowResult>=preview.deployments.resumeProjectDeployment(project,{statePath:'../project.json',timeoutMs:1000});
    // @ts-expect-error project plans are immutable
    project.runtime='node24';
    void projectRun;void projectResume;
    type ReviewedPlan=import('@gregale/sdk-node').ReviewedProjectPlan;
    type ApplicationResult=import('@gregale/sdk-node').ProjectApplicationResult;
    const review:ReviewedPlan=await client.projects.previewLocal({directory:'project',projectSlug:'shop'},{timeoutMs:1000});
    const application:Promise<ApplicationResult>=client.projects.applyPlan(review,{statePath:'../apply.json',timeoutMs:1000});
    const applicationResume:Promise<ApplicationResult>=preview.projects.resumePlanApplication(review,{statePath:'../apply.json',timeoutMs:1000});
    client.projects.applyCachedProjectPlan({requestBody:{plan_token:'reviewed'},deploymentAttemptId:'a'.repeat(32)});
    // @ts-expect-error reviewed plans are immutable
    review.preview.can_apply=false;
    // @ts-expect-error durable application requires a journal
    client.projects.applyPlan(review,{timeoutMs:1000});
    void application;void applicationResume;

    // @ts-expect-error a durable state path is required
    client.deployments.deploySource({directory:'project',appSlug:'api'},{timeoutMs:1000});
    // @ts-expect-error legacy static services retain their existing surface
    DeploymentsService.deploySource({directory:'project',appSlug:'api'}, {statePath:'../state.json',timeoutMs:1000});
    type SourceCheckpoint = import('@gregale/sdk-node').SourceUploadCheckpoint;
    const sourceOptions: import('@gregale/sdk-node').SourceUploadOptions = {timeoutMs:1000,onCheckpoint:value => {
      const bytes:number = value.received_bytes; void bytes;
      // @ts-expect-error checkpoint fields are readonly
      value.stage = 'new';
    }};
    const source:Promise<SourceCheckpoint> = client.uploads.uploadSource({path:'archive',appSlug:'api'},sourceOptions);
    // @ts-expect-error durable checkpoint callback is required
    client.uploads.uploadSource({path:'archive',appSlug:'api'},{timeoutMs:1000});
    void source;
    const waiters: DeploymentWaiters = client.deployments;
    const buildOptions: BuildWaitOptions = { timeoutMs: 1000, maxPolls: 3, onProgress: (value: WaiterSnapshot<BuildResponse>, progress: WaiterProgress) => {
      const status: BuildResponse['status'] = value.status; const poll: number = progress.poll; void status; void poll;
      // @ts-expect-error progress observations are immutable
      value.status = 'succeeded';
    } };
    const buildWait: Promise<BuildResponse> = waiters.waitForBuild({ id: 'build' }, buildOptions);
    const deployOptions: DeploymentWaitOptions = { timeoutMs: 1000, stopOnBlocked: false, onProgress: value => {
      // @ts-expect-error nested progress arrays are immutable
      value.events.push({ at: 'now', kind: 'note', summary: 'changed' });
    } };
    const deployWait: Promise<ReleaseTimeline> = preview.deployments.waitForDeployment({ id: 'deployment' }, deployOptions);
    const timeout: Error = new WaiterError('timeout', 'build', 'id', 0);
    void buildWait; void deployWait; void timeout;
    // @ts-expect-error a total wait budget is required
    waiters.waitForBuild({ id: 'build' }, {});
    // @ts-expect-error legacy static services keep their original surface
    DeploymentsService.waitForBuild({ id: 'build' }, buildOptions);
    const helpers: ExecutionHelpers = preview;
    const execution: Promise<ExecutionResponse> = helpers.runExecution({ runtime: 'node22', source: 'console.log(1)' });
    const events: AsyncGenerator<ExecutionEvent, void, void> = helpers.watchExecution('run', { after: 1 });
    void execution; void events;
    // @ts-expect-error workflow helpers remain preview-only
    client.runExecution({ runtime: 'node22' });
    // @ts-expect-error generated execution inputs remain checked
    preview.runExecution({ runtime: 'unsupported' });
    // @ts-expect-error preview APIs are not exposed by the public factory
    void client.flags;
    // @ts-expect-error generated method arguments remain checked
    client.apps.getApp({});
    // @ts-expect-error request options cannot replace required API parameters
    client.apps.getApp(options);
    // @ts-expect-error request options remain outside API parameters
    client.apps.getApp({ slug: 'typed', timeoutMs: 1000 });
    // @ts-expect-error timeout is numeric
    client.apps.getApp({ slug: 'typed' }, { timeoutMs: '1000' });
    function immutable(info: ResponseInfo) {
      // @ts-expect-error response metadata is immutable
      info.status = 500;
    }
    void immutable;
    const legacy = new FaaSClient('https://api.test');
    void AppsService.getApp({ slug: 'legacy' });
    // @ts-expect-error static legacy methods keep their original signature
    void AppsService.getApp({ slug: 'legacy' }, options);
    legacy.uninstall();
  `);
  await writeFile(join(app, 'tsconfig.json'), JSON.stringify({
    compilerOptions: {
      target: 'ES2022', module: 'NodeNext', moduleResolution: 'NodeNext', strict: true,
      noEmit: true, types: ['node'], typeRoots: [resolve(sdk, 'node_modules/@types')],
    },
    files: ['app.ts'],
  }));
  await exec(process.execPath, [resolve(sdk, 'node_modules/typescript/bin/tsc'), '-p', 'tsconfig.json'], { cwd: app });
});
