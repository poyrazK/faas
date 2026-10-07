import test from 'node:test';
import assert from 'node:assert/strict';
import { GregaleOperationClient, OperationHTTPError, type Operation } from '../src/customer-operations.js';
import { GregaleOperations } from '../src/operations-runtime.js';

const id = '11111111-1111-4111-8111-111111111111';
const invocation = '22222222-2222-4222-8222-222222222222';
const definition = '33333333-3333-4333-8333-333333333333';
const snapshot: Operation<{ file: string }> = {
  id, name: 'export', generation: 1, state: 'succeeded', result: { file: 'exports/alice.csv' },
  completion_delivery: { state: 'pending', attempts: 1, last_error: 'receiver unavailable' },
  cancellation_requested: false, latest_sequence: 2, created_at: '2026-09-30T12:00:00Z', updated_at: '2026-09-30T12:01:00Z', expires_at: '2026-10-30T12:01:00Z',
};

test('customer client retains business outcome and supplies fresh scoped credentials', async () => {
  let issued = 0; const calls: { path: string; body: unknown; token: string | null }[] = [];
  const client = new GregaleOperationClient({ apiURL: 'https://api.gregale.test', credential: () => `tenant-${++issued}`, fetch: async (url, init) => {
    calls.push({ path: new URL(String(url)).pathname, body: init?.body ? JSON.parse(String(init.body)) as unknown : undefined, token: new Headers(init?.headers).get('Authorization') });
    return Response.json(init?.method === 'POST' && !String(url).endsWith('/cancel') ? { id, status_url: `/v1/platform-tenant-self/customer-operations/${id}`, events_url: `/v1/platform-tenant-self/customer-operations/${id}/events` } : snapshot);
  } });
  assert.equal((await client.start(definition, { count: 1 }, 'export-1')).id, id);
  const result = await client.get<{ file: string }>(id);
  assert.equal(result.state, 'succeeded'); assert.equal(result.result?.file, 'exports/alice.csv'); assert.equal(result.completion_delivery.state, 'pending');
  await client.cancel(id, 1);
  assert.deepEqual(calls.map(call => call.token), ['Bearer tenant-1', 'Bearer tenant-2', 'Bearer tenant-3']);
  assert.deepEqual(calls[2]?.body, { expected_generation: 1 });
  assert.throws(() => client.start(definition, {}, ''), /idempotency/);
  assert.throws(() => client.cancel(id, 0), /generation/);
});

test('payload conflicts are surfaced without generating another operation', async () => {
  let calls = 0;
  const client = new GregaleOperationClient({ apiURL: 'https://api.gregale.test', credential: () => 'tenant', fetch: async () => { calls++; return Response.json({ code: 'operation_input_conflict' }, { status: 409 }); } });
  await assert.rejects(client.start(definition, { count: 2 }, 'same-key'), (error: unknown) => error instanceof OperationHTTPError && error.status === 409 && error.code === 'operation_input_conflict');
  assert.equal(calls, 1);
});

test('runtime report refreshes workload identity and carries the current execution fence', async () => {
  let assertions = 0; const reports: { headers: Headers; body: unknown }[] = [];
  const runtime = new GregaleOperations({ apiURL: 'https://api.gregale.test', identityEndpoint: 'http://127.0.0.1:9091/identity', fetch: async (url, init) => {
    const target = new URL(String(url));
    if (target.hostname === '127.0.0.1') { assert.equal(target.searchParams.get('audience'), 'gregale:operations'); return Response.json({ access_token: `assertion-${++assertions}` }); }
    assert.equal(target.pathname, `/v1/runtime/operations/${id}/progress`);
    reports.push({ headers: new Headers(init?.headers), body: JSON.parse(String(init?.body)) as unknown });
    return Response.json({ ...snapshot, state: 'running' });
  } });
  const context = { 'X-Gregale-Customer-Operation-Id': id, 'X-Faas-Invocation-Id': invocation, 'X-Gregale-Operation-Attempt': '2', 'X-Gregale-Operation-Capability': 'a'.repeat(64) };
  await runtime.runRequest(context, async () => {
    assert.equal(runtime.context()?.attempt, 2);
    await runtime.progress({ report_id: 'chunk-1', stage: 'generating', completed: 1, total: 3 });
    await runtime.progress({ report_id: 'chunk-1', stage: 'generating', completed: 1, total: 3 });
  });
  assert.equal(assertions, 2); assert.deepEqual(reports[0]?.body, reports[1]?.body);
  assert.equal(reports[1]?.headers.get('Authorization'), 'Bearer assertion-2');
  assert.equal(reports[0]?.headers.get('X-Faas-Invocation-Id'), invocation);
  assert.equal(reports[0]?.headers.get('X-Gregale-Operation-Attempt'), '2');
  assert.equal(runtime.context(), undefined);
  await assert.rejects(runtime.progress({ stage: 'generating', completed: 0, total: 1 }), /requires an operation/);
  assert.throws(() => runtime.runRequest({ ...context, 'X-Gregale-Operation-Capability': 'forged' }, () => {}), /Invalid operation/);
});

test('transaction callback retains the runtime progress context and releases it afterwards', async () => {
  const runtime = new GregaleOperations({ apiURL: 'https://api.gregale.test', identityEndpoint: 'http://127.0.0.1/identity', fetch: async (url, init) => {
    if (new URL(String(url)).hostname === '127.0.0.1') return Response.json({access_token: 'workload'});
    assert.equal(new Headers(init?.headers).get('X-Gregale-Operation-Capability'), 'a'.repeat(64));
    return Response.json({...snapshot, state: 'running'});
  }});
  let released = false;
  const pool = {connect: async () => ({query: async () => ({rows: []}), release: () => {released = true;}})};
  const request = {headers: {
    'X-Gregale-Customer-Operation-Id': id, 'X-Faas-Invocation-Id': invocation,
    'X-Gregale-Operation-Attempt': '1', 'X-Gregale-Operation-Capability': 'a'.repeat(64),
    'X-Gregale-Customer-Operation-Transaction-Version': '1', 'X-Gregale-Customer-Operation-Result-Max-Bytes': '65536',
    'X-Faas-Tenant-Id': definition, 'X-Faas-App-Id': definition, 'X-Faas-Platform-Tenant-Id': definition,
  }, method: 'POST', path: '/exports', body: Buffer.from('{}')};
  const result = await runtime.transaction(request, pool, async () => {
    assert.equal(runtime.context()?.id, id);
    await runtime.progress({stage: 'generating', completed: 1, total: 1});
    return {file: 'committed.csv'};
  });
  assert.deepEqual(result, {body: '{"file":"committed.csv"}', replayed: false});
  assert.equal(released, true); assert.equal(runtime.context(), undefined);
});

test('stream refreshes credentials and resumes with the last applied durable cursor', async () => {
  let tokens = 0; const requests: Headers[] = [];
  const signal = new AbortController();
  const client = new GregaleOperationClient({ apiURL: 'https://api.gregale.test', credential: () => `tenant-${++tokens}`, fetch: async (_url, init) => {
    requests.push(new Headers(init?.headers));
    const data = requests.length === 1
      ? 'event: auth_expired\ndata: {"code":"operation_credential_expired"}\n\n'
      : `id: 2\nevent: operation\ndata: ${JSON.stringify({ operation_id: id, sequence: 2, type: 'succeeded', data: { state: 'succeeded' }, created_at: snapshot.updated_at })}\n\nevent: snapshot\ndata: ${JSON.stringify(snapshot)}\n\n`;
    return new Response(data, { headers: { 'Content-Type': 'text/event-stream' } });
  } });
  const stream = client.subscribe<{ file: string }>(id, { after: 1, signal: signal.signal });
  const event = await stream.next(); assert.equal(event.value?.event?.sequence, 2);
  const result = await stream.next(); assert.equal(result.value?.snapshot?.state, 'succeeded'); assert.equal(result.value?.snapshot?.completion_delivery.state, 'pending');
  assert.equal(requests[1]?.get('Authorization'), 'Bearer tenant-2'); assert.equal(requests[1]?.get('Last-Event-ID'), '1');
  signal.abort(); await stream.return(undefined);
});

test('resync snapshots establish a new cursor and malformed event gaps stop the stream', async () => {
  const abort = new AbortController();
  let requests = 0;
  const client = new GregaleOperationClient({ apiURL: 'https://api.gregale.test', credential: () => 'tenant', fetch: async () => {
    requests++; return new Response(`event: resync\ndata: ${JSON.stringify(snapshot)}\n\nid: 4\nevent: operation\ndata: ${JSON.stringify({ operation_id:id, sequence:4 })}\n\n`, { headers:{'Content-Type':'text/event-stream'} });
  } });
  const stream = client.subscribe(id, { signal: abort.signal });
  assert.equal((await stream.next()).value?.resync, true);
  await assert.rejects(stream.next(), /cursor/);
  assert.equal(requests, 1);
});


test('concurrent HTTP handlers isolate authority and omit it from public context', async () => {
  const other = '44444444-4444-4444-8444-444444444444';
  const reports = new Map<string, string | null>();
  const runtime = new GregaleOperations({ apiURL:'https://api.gregale.test', identityEndpoint:'http://127.0.0.1/identity', fetch:async (url,init) => {
    const target = new URL(String(url));
    if (target.hostname==='127.0.0.1') return Response.json({access_token:'workload'});
    const operation = target.pathname.split('/').at(-2)!;
    reports.set(operation, new Headers(init?.headers).get('X-Gregale-Operation-Capability'));
    return Response.json({...snapshot,id:operation,state:'running'});
  }});
  await Promise.all([id,other].map(async (operation,index) => runtime.runRequest({
    'X-Gregale-Customer-Operation-Id':operation, 'X-Faas-Invocation-Id':invocation,
    'X-Gregale-Operation-Attempt':'2', 'X-Gregale-Operation-Capability':String(index ? 'b':'a').repeat(64),
  }, async () => {
    await Promise.resolve();
    assert.equal(runtime.context()?.id,operation);
    assert.ok(!JSON.stringify(runtime.context()).includes('capability'));
    await runtime.progress({report_id:'chunk-1',stage:'generating',completed:1,total:1});
  })));
  assert.equal(reports.get(id),'a'.repeat(64)); assert.equal(reports.get(other),'b'.repeat(64));
  assert.equal(runtime.context(),undefined);
});

test('HTTP runtime rejects native context without fetching an assertion', () => {
  const runtime = new GregaleOperations({apiURL:'https://api.gregale.test',identityEndpoint:'http://127.0.0.1/identity',fetch:async () => { throw Error('unexpected identity request'); }});
  const headers = {'X-Gregale-Customer-Operation-Id':id,'X-Faas-Invocation-Id':invocation,'X-Gregale-Operation-Attempt':'1','X-Gregale-Operation-Capability':'a'.repeat(64),'X-Gregale-Operation-Execution-Kind':'job'};
  assert.throws(() => runtime.runRequest(headers,()=>{}),/Invalid operation/);
});

test('history discovers scoped work without an operation ID and refreshes credentials per page', async () => {
  const app = '11111111-1111-1111-1111-111111111111';
  let calls = 0, credentials = 0;
  const client = new GregaleOperationClient({apiURL: 'https://api.example.com', credential: () => `tenant-${++credentials}`, fetch: async (input, init) => {
    const url = new URL(String(input)); calls++;
    assert.equal(url.pathname, '/v1/platform-tenant-self/customer-operations');
    assert.equal(url.searchParams.get('app_id'), app); assert.equal(url.searchParams.get('scope'), 'staging');
    assert.equal(url.searchParams.get('name'), 'customer-export'); assert.equal(url.searchParams.get('state'), 'succeeded');
    assert.equal(new Headers(init?.headers).get('Authorization'), `Bearer tenant-${calls}`);
    if (calls === 2) assert.equal(url.searchParams.get('cursor'), 'opaque+/=');
    return Response.json({operations: [], ...(calls === 1 ? {next_cursor: 'opaque+/='} : {})});
  }});
  const options = {appID: app, scope: 'staging', name: 'customer-export', state: 'succeeded' as const, limit: 1};
  const page = await client.list(options); await client.list({...options, cursor: page.next_cursor});
  assert.equal(calls, 2);
  for (const invalid of [{scope: ''}, {scope: '__all__'}, {appID: 'bad'}, {limit: 101}, {limit: 0}, {name: ''}, {cursor: ''}]) assert.throws(() => client.list({...options, ...invalid}));
  assert.equal(calls, 2);
});

test('default browser fetch retains its global receiver for customer requests', async () => {
  const original = globalThis.fetch;
  globalThis.fetch = async function (this: unknown) {
    assert.equal(this, globalThis);
    return Response.json({operations: []});
  };
  try {
    const client = new GregaleOperationClient({apiURL: 'https://api.example.com', credential: () => 'tenant'});
    assert.deepEqual(await client.list({appID: id, scope: 'default'}), {operations: []});
  } finally { globalThis.fetch = original; }
});


// ADR-639: business references stay opaque, paired and scoped by tenant credentials.
test('business reference lookup encodes exact IDs and rejects partial or oversized selectors', async () => {
  let calls = 0;
  const subject = {type: 'order', id: 'ord/42?&é😀'};
  const client = new GregaleOperationClient({apiURL: 'https://api.example.com', credential: () => 'tenant', fetch: async (input, init) => {
    calls++;
    const url = new URL(String(input));
    assert.equal(url.searchParams.get('subject_type'), subject.type);
    assert.equal(url.searchParams.get('subject_id'), subject.id);
    assert.equal(url.searchParams.get('scope'), 'default');
    assert.equal(new Headers(init?.headers).get('Authorization'), 'Bearer tenant');
    return Response.json({operations: [{id, subject, state: 'succeeded'}]});
  }});
  const options = {appID: id, scope: 'default', subjectType: subject.type, subjectID: subject.id};
  const page = await client.list(options);
  assert.deepEqual(page.operations[0]?.subject, subject);
  for (const invalid of [{subjectType: undefined}, {subjectID: undefined}, {subjectID: ''}, {subjectType: 'Order'}, {subjectID: 'é'.repeat(129)}, {subjectID: 'a\n'}, {subjectID: '\uD800'}]) {
    assert.throws(() => client.list({...options, ...invalid}), /paired business reference/);
  }
  assert.equal(calls, 1);
});

test('customer milestone feeds retain exact references and refresh scoped credentials', async () => {
  const paths: URL[] = []; let tokens=0;
  const workflowStep = {workflow:'order-lifecycle',title:'Order lifecycle',step:'paid',label:'Payment authorized',milestone:'paid',position:2};
  const client=new GregaleOperationClient({apiURL:'https://api.gregale.test',credential:()=>`tenant-${++tokens}`,fetch:async (url,init)=>{
    paths.push(new URL(String(url)));
    assert.equal(new Headers(init?.headers).get('Authorization'),`Bearer tenant-${tokens}`);
    return Response.json({milestones:[{id,operation_id:id,name:'paid',payload:{},occurred_at:'2026-10-07T12:00:00Z',created_at:'2026-10-07T12:00:01Z',sequence:1,workflow_steps:[workflowStep]}],next_cursor:'opaque'});
  }});
  await client.milestones(id,{limit:2,cursor:'next+/='});
  const page=await client.businessMilestones({appID:definition,scope:'staging',subjectType:'order',subjectID:'ord/42&é'});
  assert.deepEqual(page.milestones[0]?.workflow_steps?.[0],workflowStep);
  assert.equal(tokens,2);assert.equal(paths[0]?.pathname,`/v1/platform-tenant-self/customer-operations/${id}/milestones`);
  assert.equal(paths[0]?.searchParams.get('cursor'),'next+/=');assert.equal(paths[1]?.searchParams.get('subject_id'),'ord/42&é');
  assert.equal(paths[1]?.searchParams.get('app_id'),definition);assert.equal(paths[1]?.searchParams.get('tenant_id'),null);
  assert.throws(()=>client.businessMilestones({appID:definition,scope:'staging',subjectType:'order',subjectID:''}));
  assert.throws(()=>client.milestones(id,{limit:101}));
});
