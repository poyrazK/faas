// ADR-521: the public generated client preserves stable submissions and durable cursors.
import test from 'node:test';
import assert from 'node:assert/strict';
import { DevService, OperationsService, OpenAPI } from '../src/index.js';

test('rehearsal simulation preserves fixture acknowledgement and individual attempts', async () => {
  const previousFetch = globalThis.fetch, previousBase = OpenAPI.BASE;
  const digest = 'd'.repeat(64), runId = 'a'.repeat(32);
  OpenAPI.BASE = 'https://api.example.com';
  globalThis.fetch = async (_input, init) => {
    if (init?.method === 'PUT') {
      const body = JSON.parse(String(init.body));
      assert.equal(body.action_evidence_version, 2);
      assert.equal(body.command_version, 1);
      assert.equal(body.simulation.fixtures[0].adapter, 'resend');
      return Response.json({webhook_mode: 'capture', network_mode: 'contained', policy_version: 1, action_evidence_version: 2, simulation_sha256: digest, recorder_version: 1, command_version: 1});
    }
    return Response.json({version: 2, scope: 'managed_webhooks_and_simulated_outbound', simulation_version: 1, simulation_sha256: digest, recorder_version: 1, recorder_sessions: 1, pending_recorder_sessions: 0, incomplete_recorder_sessions: 0, run_id: runId, status: 'complete', sealed_at: '2026-10-17T12:00:00Z', workloads: ['primary'], intents: [], captured_count: 0, pending_managed_work: 0, incomplete_managed_effects: 0, suppressed_deliveries: 0, truncated: false, simulated_attempts: [
      {id: '11111111-1111-4111-8111-111111111111', workload: 'primary', fixture: 'notify', integration: 'email', adapter: 'resend', method: 'POST', path: '/emails', request_sha256: 'e'.repeat(64), payload: {subject: 'Import ready'}, attempt: 1, status: 'completed', response_status: 429},
      {id: '22222222-2222-4222-8222-222222222222', workload: 'primary', fixture: 'notify', integration: 'email', adapter: 'resend', method: 'POST', path: '/emails', request_sha256: 'e'.repeat(64), payload: {subject: 'Import ready'}, attempt: 2, status: 'completed', response_status: 200},
    ]});
  };
  try {
    const ack = await DevService.registerScenarioTest({runId, requestBody: {command_version: 1, webhook_mode: 'capture', network_mode: 'contained', action_evidence_version: 2, members: [{workload: 'primary', app_slug: 'isolated-api'}], simulation: {version: 1, fixtures: [{workload: 'primary', name: 'notify', integration: 'email', adapter: 'resend', method: 'POST', path: '/emails', responses: [{status: 200, body: '{"id":"simulated-1"}'}]}]}}});
    assert.equal(ack.simulation_sha256, digest);
    assert.equal(ack.recorder_version, 1);
    assert.equal(ack.command_version, 1);
    const snapshot = await DevService.sealScenarioTestActions({runId});
    assert.equal(snapshot.version, 2);
    assert.equal(snapshot.simulation_version, 1);
    assert.equal(snapshot.recorder_version, 1);
    assert.equal(snapshot.recorder_sessions, 1);
    assert.equal(snapshot.pending_recorder_sessions, 0);
    assert.deepEqual(snapshot.simulated_attempts?.map(a => [a.attempt, a.response_status]), [[1, 429], [2, 200]]);
  } finally { globalThis.fetch = previousFetch; OpenAPI.BASE = previousBase; }
});

test('rehearsal capture negotiates sealed evidence and preserves pending inventory', async () => {
  const previousFetch = globalThis.fetch, previousBase = OpenAPI.BASE, previousToken = OpenAPI.TOKEN;
  const runId = 'a'.repeat(32);
  const calls: {url: string; method: string | undefined; body: unknown}[] = [];
  OpenAPI.BASE = 'https://api.example.com'; OpenAPI.TOKEN = 'account-operator';
  globalThis.fetch = async (input, init) => {
    assert.equal(new Headers(init?.headers).get('Authorization'), 'Bearer account-operator');
    calls.push({url: String(input), method: init?.method, body: init?.body ? JSON.parse(String(init.body)) : undefined});
    const sealing = init?.method === 'POST';
    const body = sealing
      ? {version: 1, scope: 'managed_operation_webhooks', run_id: runId, status: 'pending', workloads: ['primary'], intents: [], captured_count: 0, pending_managed_work: 1, incomplete_managed_effects: 0, suppressed_deliveries: 0, truncated: false}
      : {webhook_mode: 'capture', network_mode: 'contained', policy_version: 1, action_evidence_version: 1};
    return new Response(JSON.stringify(body), {status: sealing ? 202 : 200, headers: {'Content-Type': 'application/json'}});
  };
  try {
    const ack = await DevService.registerScenarioTest({runId, requestBody: {webhook_mode: 'capture', network_mode: 'contained', action_evidence_version: 1, members: [{workload: 'primary', app_slug: 'isolated-api'}]}});
    assert.equal(ack.action_evidence_version, 1);
    assert.equal(ack.network_mode, 'contained');
    const snapshot = await DevService.sealScenarioTestActions({runId});
    assert.equal(snapshot.status, 'pending');
    assert.equal(snapshot.pending_managed_work, 1);
    assert.equal(snapshot.scope, 'managed_operation_webhooks');
    assert.deepEqual(calls.map(c => [new URL(c.url).pathname, c.method]), [
      [`/v1/dev/test-runs/${runId}`, 'PUT'], [`/v1/dev/test-runs/${runId}/actions/seal`, 'POST'],
    ]);
    assert.equal((calls[0]!.body as {action_evidence_version: number}).action_evidence_version, 1);
    assert.equal((calls[0]!.body as {network_mode: string}).network_mode, 'contained');
  } finally { globalThis.fetch = previousFetch; OpenAPI.BASE = previousBase; OpenAPI.TOKEN = previousToken; }
});

test('recovery inspection and preview preserve read-only proposals and revision fences', async () => {
  const previousFetch = globalThis.fetch, previousBase = OpenAPI.BASE, previousToken = OpenAPI.TOKEN;
  const calls: {url: string; method: string | undefined; body: unknown}[] = [];
  OpenAPI.BASE = 'https://api.example.com'; OpenAPI.TOKEN = 'account-operator';
  const revision = 'sha256:' + 'a'.repeat(64);
  globalThis.fetch = async (input, init) => {
    assert.equal(new Headers(init?.headers).get('Authorization'), 'Bearer account-operator');
    calls.push({url: String(input), method: init?.method, body: init?.body ? JSON.parse(String(init.body)) : undefined});
    return new Response(JSON.stringify({operation_id:'operation',inspection_revision:revision,eligible:false,evidence_required:true,blockers:['workflow_concurrency_limit'],reopened_steps:['finish']}),{headers:{'Content-Type':'application/json'}});
  };
  try {
    const inspection = await OperationsService.inspectOperationRecovery({slug:'exports',id:'operation'});
    assert.equal(inspection.inspection_revision, revision);
    const p = await OperationsService.previewOperationRecovery({slug:'exports',id:'operation',requestBody:{expected_generation:1,resolution:'safe_to_retry'}});
    assert.equal(p.eligible,false); assert.equal(p.evidence_required,true); assert.deepEqual(p.reopened_steps,['finish']);
    assert.deepEqual(calls[1]!.body,{expected_generation:1,resolution:'safe_to_retry'});
    await OperationsService.recoverOperation({slug:'exports',id:'operation',requestBody:{recovery_id:'decision',expected_generation:1,resolution:'safe_to_retry',evidence:'provider ledger checked',expected_inspection_revision:revision}});
    assert.equal((calls[2]!.body as {expected_inspection_revision:string}).expected_inspection_revision,revision);
    assert.deepEqual(calls.map(c=>[new URL(c.url).pathname,c.method]),[
      ['/v1/apps/exports/operations/operation/recovery-inspection','GET'],
      ['/v1/apps/exports/operations/operation/recovery-preview','POST'],
      ['/v1/apps/exports/operations/operation/recover','POST'],
    ]);
  } finally {globalThis.fetch=previousFetch;OpenAPI.BASE=previousBase;OpenAPI.TOKEN=previousToken;}
});

test('HTTP Operations contract uses customer credentials, stable keys and resume cursors', async () => {
  const previousFetch = globalThis.fetch;
  const previousBase = OpenAPI.BASE;
  const previousToken = OpenAPI.TOKEN;
  const calls: { url: string; headers: Headers }[] = [];
  OpenAPI.BASE = 'https://api.example.com';
  OpenAPI.TOKEN = 'customer-token';
  globalThis.fetch = async (input, init) => {
    calls.push({ url: String(input), headers: new Headers(init?.headers) });
    return new Response(JSON.stringify({ id: 'export-id', latest_sequence: 42, events: [] }), {
      headers: { 'Content-Type': 'application/json' },
    });
  };
  try {
    const receipt = await OperationsService.startPlatformTenantSelfOperation({
      idempotencyKey: 'stable-export', requestBody: { definition_id: 'definition-id', input: { count: 1 } },
    });
    assert.equal(receipt.id, 'export-id');
    assert.equal(calls[0]?.headers.get('Idempotency-Key'), 'stable-export');
    assert.equal(calls[0]?.headers.get('Authorization'), 'Bearer customer-token');
    assert.equal(calls[0]?.url, 'https://api.example.com/v1/platform-tenant-self/customer-operations');
    await OperationsService.getPlatformTenantSelfOperationEvents({ id: 'export-id', after: 42 });
    assert.equal(calls[1]?.url, 'https://api.example.com/v1/platform-tenant-self/customer-operations/export-id/events?after=42');
    await OperationsService.listPlatformTenantSelfOperations({appId: 'app-id', scope: 'staging', name: 'customer-export', state: 'succeeded', limit: 2, cursor: 'opaque+/='});
    const list = new URL(calls[2]!.url);
    assert.equal(list.searchParams.get('app_id'), 'app-id'); assert.equal(list.searchParams.get('scope'), 'staging');
    assert.equal(list.searchParams.get('cursor'), 'opaque+/='); assert.equal(calls[2]?.headers.get('Authorization'), 'Bearer customer-token');
  } finally {
    globalThis.fetch = previousFetch;
    OpenAPI.BASE = previousBase;
    OpenAPI.TOKEN = previousToken;
  }
});

test('account operator Operations contract preserves filters and separate delivery outcome', async () => {
  const previousFetch = globalThis.fetch, previousBase = OpenAPI.BASE, previousToken = OpenAPI.TOKEN;
  const calls: string[] = [];
  OpenAPI.BASE = 'https://api.example.com'; OpenAPI.TOKEN = 'account-operator';
  globalThis.fetch = async (input, init) => {
    assert.equal(new Headers(init?.headers).get('Authorization'), 'Bearer account-operator');
    calls.push(String(input));
    return new Response(JSON.stringify({id:'operation',state:'succeeded',generation:2,completion_delivery:{state:'pending',attempts:0}}),{headers:{'Content-Type':'application/json'}});
  };
  try {
    await OperationsService.listAccountOperations({slug:'exports',scope:'production',tenantId:'customer',limit:2,cursor:'opaque+/='});
    const list = new URL(calls[0]!);
    assert.equal(list.pathname,'/v1/apps/exports/operations');
    assert.equal(list.searchParams.get('tenant_id'),'customer'); assert.equal(list.searchParams.get('cursor'),'opaque+/=');
    assert.equal(list.searchParams.has('app_id'),false);
    await OperationsService.getAccountOperationEvents({slug:'exports',id:'operation',after:7});
    assert.equal(new URL(calls[1]!).searchParams.get('after'),'7');
    await OperationsService.getOperationExecutions({slug:'exports',id:'operation',after:1,limit:2});
    assert.equal(new URL(calls[2]!).pathname,'/v1/apps/exports/operations/operation/executions');
    const operation = await OperationsService.retryOperationDelivery({slug:'exports',id:'operation'});
    assert.equal(operation.state,'succeeded'); assert.equal(operation.generation,2); assert.equal(operation.completion_delivery.state,'pending');
    assert.equal(new URL(calls[3]!).pathname,'/v1/apps/exports/operations/operation/retry-delivery');
  } finally {globalThis.fetch=previousFetch;OpenAPI.BASE=previousBase;OpenAPI.TOKEN=previousToken;}
});

test('definition discovery keeps deployment identity and submission receipt identity', async () => {
  const previousFetch=globalThis.fetch, previousBase=OpenAPI.BASE, previousToken=OpenAPI.TOKEN;
  const calls:string[]=[];
  OpenAPI.BASE='https://api.example.com';OpenAPI.TOKEN='scoped-credential';
  globalThis.fetch=async(input,init)=>{
    assert.equal(new Headers(init?.headers).get('Authorization'),'Bearer scoped-credential');
    calls.push(String(input));
    return new Response(JSON.stringify({definitions:[{id:'definition',name:'export',deployment_id:'deployment'}],id:'definition',account_id:'account',platform_tenant_id:'tenant'}),{headers:{'Content-Type':'application/json'}});
  };
  try {
    const page=await OperationsService.listOperationDefinitions({slug:'exports',deploymentId:'deployment'});
    assert.equal(page.definitions[0]?.deployment_id,'deployment');
    await OperationsService.getOperationDefinition({slug:'exports',deploymentId:'deployment',name:'export'});
    const identity=await OperationsService.getPlatformTenantSelfOperationIdentity();
    assert.equal(identity.platform_tenant_id,'tenant');
    assert.deepEqual(calls,[
      'https://api.example.com/v1/apps/exports/deployments/deployment/operation-definitions',
      'https://api.example.com/v1/apps/exports/deployments/deployment/operation-definitions/export',
      'https://api.example.com/v1/platform-tenant-self/customer-operations/identity',
    ]);
  } finally {globalThis.fetch=previousFetch;OpenAPI.BASE=previousBase;OpenAPI.TOKEN=previousToken;}
});

test('Operations doctor reads scoped prerequisites and preserves delivery warnings', async () => {
  // ADR-521: one API observation cannot attest runtime/fleet qualification.
  const previousFetch = globalThis.fetch, previousBase = OpenAPI.BASE, previousToken = OpenAPI.TOKEN;
  let requests = 0;
  OpenAPI.BASE = 'https://api.example.com'; OpenAPI.TOKEN = 'operator-token';
  globalThis.fetch = async (input, init) => {
    requests++;
    const url = new URL(String(input));
    assert.equal(init?.method, 'GET');
    assert.equal(url.pathname, '/v1/apps/exports/deployments/deployment-id/operation-doctor');
    assert.equal(url.searchParams.get('tenant_id'), 'tenant+selector');
    assert.equal(url.searchParams.get('name'), 'export name');
    assert.equal(new Headers(init?.headers).get('Authorization'), 'Bearer operator-token');
    return new Response(JSON.stringify({ app_id: 'app', scope: 'production', deployment_id: 'deployment-id', platform_tenant_id: 'tenant+selector', plan: 'pro', observed_at: '2026-10-05T13:00:00Z', observation_scope: 'responding_api_node', submission_state: 'eligible', checks: [{ check: 'completion_destination', status: 'warning', impact: 'delivery', code: 'completion_destination_disabled', message: 'Disabled.' }, { check: 'native_lifecycle', status: 'unknown', impact: 'qualification', code: 'native_lifecycle_unverified', message: 'Unverified.' }, {check: 'execution_preview', status: 'observed', impact: 'submission', code: 'preview_cohort_observed', message: 'Job allowed.', name: 'export name', execution_kind: 'job'}] }), {headers: {'Content-Type': 'application/json'}});
  };
  try {
    const report = await OperationsService.getOperationDoctor({slug: 'exports', deploymentId: 'deployment-id', tenantId: 'tenant+selector', name: 'export name'});
    assert.equal(requests, 1); assert.equal(report.submission_state, 'eligible');
    assert.equal(report.checks[0]?.status, 'warning'); assert.equal(report.checks[1]?.status, 'unknown');
    assert.equal(report.checks[2]?.execution_kind, 'job');
  } finally { globalThis.fetch = previousFetch; OpenAPI.BASE = previousBase; OpenAPI.TOKEN = previousToken; }
});

// ADR-521: retries use an explicit transport generation and immutable decision identity.
test('completion inspection and retry receipts preserve independent transport state', async () => {
  const previousFetch = globalThis.fetch, previousBase = OpenAPI.BASE, previousToken = OpenAPI.TOKEN;
  const calls: {url: string; body: unknown}[] = [];
  OpenAPI.BASE = 'https://api.example.com'; OpenAPI.TOKEN = 'account-operator';
  globalThis.fetch = async (input, init) => {
    assert.equal(new Headers(init?.headers).get('Authorization'), 'Bearer account-operator');
    calls.push({url: String(input), body: init?.body ? JSON.parse(String(init.body)) : undefined});
    return new Response(JSON.stringify(String(input).endsWith('/delivery-retries') ? {operation_id:'operation',retry_id:'stable',delivery_id:'delivery',expected_replay_generation:0,replay_generation:1,state:'queued'} : {operation_id:'operation',business_state:'succeeded',state:'dead',replay_generation:0,attempts:[]}), {headers:{'Content-Type':'application/json'}});
  };
  try {
    const report = await OperationsService.getOperationDelivery({slug:'exports',id:'operation'});
    assert.equal(report.business_state,'succeeded'); assert.equal(report.state,'dead'); assert.equal(report.replay_generation,0);
    await OperationsService.getOperationDeliveryAttempts({slug:'exports',id:'operation',limit:1,cursor:'opaque+/='});
    const url = new URL(calls[1]!.url); assert.equal(url.pathname,'/v1/apps/exports/operations/operation/delivery-attempts'); assert.equal(url.searchParams.get('cursor'),'opaque+/=');
    const r = await OperationsService.retryOperationDeliveryWithReceipt({slug:'exports',id:'operation',requestBody:{retry_id:'stable',delivery_id:'delivery',expected_replay_generation:0}});
    assert.equal(r.state,'queued'); assert.equal(r.replay_generation,1);
    assert.deepEqual(calls[2]!.body,{retry_id:'stable',delivery_id:'delivery',expected_replay_generation:0});
    assert.equal(calls[2]!.url,'https://api.example.com/v1/apps/exports/operations/operation/delivery-retries');
  } finally {globalThis.fetch=previousFetch;OpenAPI.BASE=previousBase;OpenAPI.TOKEN=previousToken;}
});

test('workflow Operations keep named definitions and native execution identities', async () => {
  const previousFetch = globalThis.fetch, previousBase = OpenAPI.BASE, previousToken = OpenAPI.TOKEN;
  OpenAPI.BASE = 'https://api.example.com'; OpenAPI.TOKEN = 'account-operator';
  const definition = {name:'export',workflow:'export-chain',method:'POST' as const,path:'/exports',owner:'platform_tenant' as const,input_schema:true,output_schema:true,progress_stages:['collect','finish'],recovery:'reconcile_on_unknown' as const};
  globalThis.fetch = async (_input, init) => {
    if (init?.method === 'PUT') {
      assert.deepEqual(JSON.parse(String(init.body)), definition);
      return new Response(JSON.stringify({id:'definition',spec:definition}),{headers:{'Content-Type':'application/json'}});
    }
    return new Response(JSON.stringify({executions:[{generation:2,workflow_run_id:'run',state:'succeeded',attempts:3,created_at:'2026-10-06T00:00:00Z'}]}),{headers:{'Content-Type':'application/json'}});
  };
  try {
    const resolved=await OperationsService.putOperationDefinition({slug:'exports',deploymentId:'deployment',name:'export',requestBody:definition});
    assert.equal(resolved.spec.workflow,'export-chain');
    const page=await OperationsService.getOperationExecutions({slug:'exports',id:'operation'});
    assert.equal(page.executions[0]?.workflow_run_id,'run');
    assert.equal(page.executions[0]?.invocation_id,undefined);
  } finally { globalThis.fetch=previousFetch; OpenAPI.BASE=previousBase; OpenAPI.TOKEN=previousToken; }
});


test('simulation recorder closing preserves pending coverage with an empty attempt inventory', async () => {
  const previousFetch = globalThis.fetch, previousBase = OpenAPI.BASE;
  OpenAPI.BASE = 'https://api.example.com';
  const wire = {version: 2, scope: 'managed_webhooks_and_simulated_outbound', simulation_version: 1,
    simulation_sha256: 'd'.repeat(64), recorder_version: 1, recorder_sessions: 1,
    pending_recorder_sessions: 1, incomplete_recorder_sessions: 0, run_id: 'a'.repeat(32),
    status: 'pending', workloads: ['primary'], intents: [], captured_count: 0,
    pending_managed_work: 0, incomplete_managed_effects: 0, suppressed_deliveries: 0, truncated: false};
  globalThis.fetch = async () => Response.json(wire, {status: 202});
  try {
    const snapshot = await DevService.sealScenarioTestActions({runId: 'a'.repeat(32)});
    assert.deepEqual(snapshot, wire);
  } finally { globalThis.fetch = previousFetch; OpenAPI.BASE = previousBase; }
});
