// ADR-521: the public generated client preserves stable submissions and durable cursors.
import test from 'node:test';
import assert from 'node:assert/strict';
import { OperationsService, OpenAPI } from '../src/index.js';

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
    return new Response(JSON.stringify({ app_id: 'app', scope: 'production', deployment_id: 'deployment-id', platform_tenant_id: 'tenant+selector', plan: 'pro', observed_at: '2026-10-05T13:00:00Z', observation_scope: 'responding_api_node', submission_state: 'eligible', checks: [{ check: 'completion_destination', status: 'warning', impact: 'delivery', code: 'completion_destination_disabled', message: 'Disabled.' }, { check: 'native_lifecycle', status: 'unknown', impact: 'qualification', code: 'native_lifecycle_unverified', message: 'Unverified.' }] }), {headers: {'Content-Type': 'application/json'}});
  };
  try {
    const report = await OperationsService.getOperationDoctor({slug: 'exports', deploymentId: 'deployment-id', tenantId: 'tenant+selector', name: 'export name'});
    assert.equal(requests, 1); assert.equal(report.submission_state, 'eligible');
    assert.equal(report.checks[0]?.status, 'warning'); assert.equal(report.checks[1]?.status, 'unknown');
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
