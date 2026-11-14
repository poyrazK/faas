import test from 'node:test';
import assert from 'node:assert/strict';
import { createIssueReporter } from '../src/issues.js';

test('HTTP status context is validated and delivered', async () => {
  const sent: Array<Record<string, unknown>> = [];
  const reporter = createIssueReporter({baseURL:'https://api.gregale.dev',app:'exports',token:'g_issue_test',
    groupingStrategy:'failure-site-v1',
    fetch:(async (_url, options) => {sent.push(JSON.parse(String(options?.body)));return new Response('{}',{status:202});}) as typeof fetch});
  assert.ok(reporter.captureException(new Error('first'), {route:'/checkout',http_status:502}));
  assert.ok(reporter.captureException(new Error('second'), {http_status:200}));
  assert.equal(await reporter.close(), true);
  assert.equal(sent[0]?.http_status, 502);
  assert.equal(sent[1]?.http_status, undefined);
});

test('capture isolates throwing error properties and uses a valid fallback name', async () => {
  const sent: any[] = [];
  const reporter = createIssueReporter({baseURL:'https://api.gregale.dev',app:'exports',token:'g_issue_test',
    fetch:(async (_url, options) => {sent.push(JSON.parse(String(options?.body)));return new Response('{}',{status:202});}) as typeof fetch});
  const broken = new Error('original');
  Object.defineProperty(broken, 'stack', {get() { throw new Error('stack getter failed'); }});
  assert.equal(reporter.captureException(broken), undefined);
  assert.equal(reporter.stats().dropped, 1);
  const blank = new Error('failure'); blank.name = '';
  assert.ok(reporter.captureException(blank));
  assert.equal(await reporter.close(), false); // the earlier capture was dropped
  assert.equal(sent[0].exception_type, 'Error');
});

test('breadcrumbs copy only documented fields, including for cyclic extra data', async () => {
  let event: any;
  const reporter = createIssueReporter({baseURL:'https://api.gregale.dev',app:'exports',token:'g_issue_test',
    fetch:(async (_url, options) => {event=JSON.parse(String(options?.body));return new Response('{}',{status:202});}) as typeof fetch});
  const extra: {self?: unknown} = {};
  extra.self = extra;
  const breadcrumb = {occurred_at:new Date().toISOString(),category:'custom' as const,level:'info' as const,message:'entry',extra};
  assert.ok(reporter.captureException(new Error('failure'), {breadcrumbs:[breadcrumb],invocation_id:'00000000-0000-0000-0000-000000000000'}));
  assert.equal(await reporter.close(), true);
  assert.deepEqual(Object.keys(event.breadcrumbs[0]).sort(), ['category','level','message','occurred_at']);
  assert.equal(event.invocation_id, undefined);
});

test('flush and close report permanently rejected events', async () => {
  const reporter = createIssueReporter({baseURL:'https://api.gale.dev',app:'exports',token:'g_issue_test',
    fetch:(async () => new Response('', {status:401})) as typeof fetch});
  reporter.captureException(new Error('rejected'));
  assert.equal(await reporter.flush(), false);
  assert.equal(await reporter.close(0), false);
  assert.equal(reporter.stats().dropped, 1);
});

for (const status of [429, 503]) test(`${status} honors Retry-After even on an explicit flush`, async () => {
  let attempts = 0;
  const reporter = createIssueReporter({baseURL:'https://api.gregale.dev',app:'exports',token:'g_issue_test',
    fetch:(async () => { attempts++; return new Response('', {status, headers:{'Retry-After':'60'}}); }) as typeof fetch});
  reporter.captureException(new Error('limited'));
  assert.equal(await reporter.flush(), false);
  assert.equal(await reporter.flush(), false);
  assert.equal(attempts, 1);
  assert.equal(await reporter.close(0), false);
});

test('close retries when the retry delay fits inside its deadline', async () => {
  let attempts = 0;
  const reporter = createIssueReporter({baseURL:'https://api.gregale.dev',app:'exports',token:'g_issue_test',
    fetch:(async () => ++attempts === 1 ? new Response('', {status:429,headers:{'Retry-After':'1'}}) : new Response('',{status:202})) as typeof fetch});
  reporter.captureException(new Error('limited'));
  assert.equal(await reporter.flush(), false);
  assert.equal(await reporter.close(3000), true);
  assert.equal(attempts, 2);
  assert.equal(reporter.stats().queued, 0);
});

test('exception reporting retries the same event and keeps request context', async () => {
  const requests: Array<Record<string, unknown>> = [];
  let attempt = 0;
  const reporter = createIssueReporter({ baseURL: 'https://api.gregale.dev', app: 'exports', token: 'g_issue_test',
    fetch: (async (_url, options) => {
      requests.push(JSON.parse(String(options?.body)));
      assert.equal(new Headers(options?.headers).get('Authorization'), 'Bearer g_issue_test');
      if (attempt++ === 0) throw new Error('connection failed');
      return new Response('{}', { status: 202 });
    }) as typeof fetch });
  const id = reporter.captureException(new TypeError('invalid format'), { request_id: 'request', route: '/exports' });
  assert.equal(await reporter.flush(), false);
  assert.equal(await reporter.flush(), false);
  assert.equal(await reporter.close(3000), true);
  assert.equal(requests.length, 2);
  assert.equal(requests[0]?.event_id, id);
  assert.deepEqual(requests[0], requests[1]);
  assert.equal(requests[0]?.request_id, 'request');
  assert.ok(Array.isArray(requests[0]?.frames));
  assert.equal(requests[0]?.grouping_strategy, undefined);
  assert.equal(requests[0]?.frame_order, undefined);
  await reporter.close();
});

test('failure-site reporting preserves bounded grouping controls across retries', async () => {
  const requests: Array<Record<string, unknown>> = [];
  const reporter = createIssueReporter({
    baseURL: 'https://api.gregale.dev', app: 'exports', token: 'g_issue_test',
    groupingStrategy: 'failure-site-v1',
    fetch: (async (_url, options) => {
      requests.push(JSON.parse(String(options?.body)));
      return new Response('{}', { status: requests.length === 1 ? 503 : 202 });
    }) as typeof fetch,
  });
  const error = new Error('invalid date');
  error.stack = 'Error: invalid date\n    at parseDate (/app/date.js:10:5)\n    at exportHandler (/app/exports.js:20:5)';
  reporter.captureException(error, {
    error_code: 'INVALID_DATE', fingerprint_override: 'é'.repeat(200),
  });
  assert.equal(await reporter.flush(), false);
  assert.equal(await reporter.flush(), false);
  assert.equal(await reporter.close(3000), true);
  assert.deepEqual(requests[0], requests[1]);
  assert.equal(requests[0]?.grouping_strategy, 'failure-site-v1');
  assert.equal(requests[0]?.frame_order, 'newest-first');
  assert.equal(requests[0]?.error_code, 'INVALID_DATE');
  assert.equal(requests[0]?.fingerprint_override, 'é'.repeat(128));
  assert.equal((requests[0]?.frames as Array<{function: string}>)[0]?.function, 'parseDate');
  await reporter.close();
});

test('invalid grouping strategy fails before starting a reporter', () => {
  assert.throws(() => createIssueReporter({
    baseURL: 'https://api.gregale.dev', app: 'exports', token: 'g_issue_test',
    groupingStrategy: 'unknown' as 'failure-site-v1',
  }), /grouping strategy/);
});

test('queue overflow is visible and wrapping preserves the original failure', async () => {
  const reporter = createIssueReporter({ baseURL: 'https://api.gregale.dev', app: 'exports', token: 'g_issue_test', maxQueue: 1,
    fetch: (async () => new Response('{}', { status: 503 })) as typeof fetch });
  const original = new Error('original');
  await assert.rejects(reporter.wrap(async () => { throw original; }), error => error === original);
  assert.equal(reporter.captureException(new Error('overflow')), undefined);
  assert.equal(reporter.stats().dropped, 1);
  assert.equal(await reporter.close(0), false);
});

test('queue and request timeout options must be finite', () => {
  const base = {baseURL:'https://api.gregale.dev',app:'exports',token:'g_issue_test'};
  for (const value of [NaN, Infinity, -Infinity]) {
    assert.throws(() => createIssueReporter({...base,maxQueue:value}), /queue size/);
    assert.throws(() => createIssueReporter({...base,timeoutMs:value}), /timeout/);
  }
});

test('oversized escaped frames and context fit the ingest envelope', async () => {
  let event: any;
  const reporter = createIssueReporter({baseURL:'https://api.gregale.dev',app:'exports',token:'g_issue_test',
    fetch:(async (_url, options) => {
      const body = String(options?.body);
      assert.ok(Buffer.byteLength(body) <= 65536);
      event = JSON.parse(body);
      return new Response('', {status:202});
    }) as typeof fetch});
  const error = new Error('test');
  const text = '\u0001'.repeat(490);
  error.stack = 'Error: test\n' + Array.from({length:32},()=>`    at ${text} (/app/${text}.js:1:1)`).join('\n');
  assert.ok(reporter.captureException(error, {route:'é'.repeat(300),request_id:'r'.repeat(300),trace_id:'bad',span_id:'0'.repeat(16),invocation_id:'bad'}));
  assert.equal(await reporter.flush(), true);
  assert.equal(Buffer.byteLength(event.route),256);
  assert.equal(Buffer.byteLength(event.request_id),256);
  assert.equal(event.trace_id, undefined);
  assert.equal(event.span_id, undefined);
  assert.equal(event.invocation_id, undefined);
  assert.ok(event.frames.length >= 1);
  assert.equal(await reporter.close(), true);
});

test('optional context cannot change retained grouping evidence', async () => {
  const events: any[] = [];
  const reporter = createIssueReporter({baseURL:'https://api.gregale.dev',app:'exports',token:'g_issue_test',
    fetch:(async (_url, options) => { events.push(JSON.parse(String(options?.body))); return new Response('',{status:202}); }) as typeof fetch});
  const error = new Error('same error');
  const text = 'x'.repeat(400) + '\u0001'.repeat(60);
  error.stack = 'Error: same error\n' + Array.from({length:32},()=>`    at ${text} (/app/${text}.js:1:1)`).join('\n');
  const breadcrumbs = Array.from({length:16},()=>({occurred_at:new Date().toISOString(),category:'custom' as const,level:'info' as const,message:'\u0001'.repeat(256)}));
  reporter.captureException(error);
  reporter.captureException(error, {breadcrumbs, tags:{component:'billing'}, route:'\u0001'.repeat(256)});
  assert.equal(await reporter.close(), true);
  assert.equal(events.length, 2);
  assert.deepEqual(events[0].frames, events[1].frames);
  assert.equal(events[0].causes_incomplete, events[1].causes_incomplete);
});

test('invalid breadcrumbs are dropped and allowed tags survive unknown keys', async () => {
  let event: any;
  const reporter = createIssueReporter({baseURL:'https://api.gregale.dev',app:'exports',token:'g_issue_test',
    fetch:(async (_url, options) => { event = JSON.parse(String(options?.body)); return new Response('',{status:202}); }) as typeof fetch});
  const breadcrumbs = ['yesterday','2026-02-30T10:00:00Z','0001-01-01T00:00:00Z',new Date(Date.now()+600000).toISOString(),new Date().toISOString()]
    .map(occurred_at => ({occurred_at,category:'custom' as const,level:'info' as const,message:'entry'}));
  const tags = Object.fromEntries([...Array.from({length:16},(_,i)=>[`junk${i}`,'v']),['component','billing']]);
  reporter.captureException(new Error('context'), {breadcrumbs,tags});
  assert.equal(await reporter.close(), true);
  assert.equal(event.breadcrumbs.length, 1);
  assert.equal(event.breadcrumbs_dropped, 4);
  assert.deepEqual(event.tags, {component:'billing'});
});

test('close bounds and aborts an in-flight delivery', async () => {
  let started!: () => void;
  const entered = new Promise<void>(resolve => { started = resolve; });
  let requestSignal: AbortSignal | undefined;
  const reporter = createIssueReporter({baseURL:'https://api.gregale.dev',app:'exports',token:'g_issue_test',
    fetch:(async (_url, options) => { requestSignal = options?.signal ?? undefined; started(); return await new Promise<Response>(() => {}); }) as typeof fetch});
  reporter.captureException(new Error('slow'));
  await entered;
  const start = Date.now();
  assert.equal(await reporter.close(30), false);
  assert.ok(Date.now() - start < 1000);
  assert.equal(requestSignal?.aborted, true);
  assert.equal(reporter.stats().queued, 1);
});

test('cause reporting preserves root evidence and bounds cycles, aggregates and access failures', async () => {
  const received: any[] = [];
  const reporter = createIssueReporter({baseURL:'https://api.gregale.dev',app:'exports',token:'g_issue_test',groupingStrategy:'cause-chain-v1',
    fetch:(async (_url, options) => {received.push(JSON.parse(String(options?.body)));return new Response('{}',{status:202});}) as typeof fetch});
  const root = Object.assign(new TypeError('root failure'), {code:'ROOT_CODE'});
  root.stack = 'TypeError: root\n    at query (/app/db.ts:10:2)';
  reporter.captureException(new Error('wrapper',{cause:root}));
  const cycle = new Error('cycle');cycle.cause=cycle;reporter.captureException(cycle);
  reporter.captureException(new AggregateError([root,new Error('other')],'aggregate'));
  const broken = new Error('broken');Object.defineProperty(broken,'cause',{get(){throw new Error('getter');}});reporter.captureException(broken);
  let deep:Error=root;for(let i=0;i<10;i++)deep=new Error('wrapper',{cause:deep});reporter.captureException(deep);
  assert.equal(await reporter.close(),true);
  assert.equal(received[0].causes[0].exception_type,'TypeError');
  assert.equal(received[0].causes[0].error_code,'ROOT_CODE');
  assert.equal(received[0].causes[0].frames[0].function,'query');
  assert.equal(received[0].causes_incomplete,undefined);
  for(const event of received.slice(1))assert.equal(event.causes_incomplete,true);
  assert.equal(received[4].causes.length,8);
});

test('oversized cause evidence is marked incomplete and stays within the ingest envelope', async () => {
  let event: any;
  const reporter=createIssueReporter({baseURL:'https://api.gregale.dev',app:'exports',token:'g_issue_test',groupingStrategy:'cause-chain-v1',
    fetch:(async (_url,options)=>{assert.ok(Buffer.byteLength(String(options?.body))<=65536);event=JSON.parse(String(options?.body));return new Response('{}',{status:202});}) as typeof fetch});
  let error=new Error('root');
  const stack='Error: failure\n'+Array.from({length:32},()=>`    at ${'f'.repeat(512)} (/app/${'x'.repeat(490)}.js:1:1)`).join('\n');
  for(let i=0;i<8;i++){error.stack=stack;error=new Error('wrapper',{cause:error});}
  error.stack=stack;reporter.captureException(error);
  assert.equal(await reporter.close(),true);
  assert.equal(event.causes_incomplete,true);
  assert.ok(event.causes.length<8);
});

test('new captures and explicit flushes respect the retry delay after a server failure', async () => {
  let attempts = 0;
  const reporter = createIssueReporter({baseURL:'https://api.gregale.dev',app:'exports',token:'g_issue_test',
    fetch:(async () => { attempts++; return new Response('', {status:500}); }) as typeof fetch});
  reporter.captureException(new Error('first'));
  assert.equal(await reporter.flush(), false);
  for (let i = 0; i < 10; i++) {
    reporter.captureException(new Error('subsequent'));
    await new Promise(resolve => setTimeout(resolve, 10));
  }
  assert.equal(await reporter.flush(), false);
  assert.equal(attempts, 1);
  assert.equal(await reporter.close(0), false);
});
