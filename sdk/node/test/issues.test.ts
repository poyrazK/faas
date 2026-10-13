import test from 'node:test';
import assert from 'node:assert/strict';
import { createIssueReporter } from '../src/issues.js';

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
  assert.equal(await reporter.flush(), true);
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
  assert.equal(await reporter.flush(), true);
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
  assert.equal(await reporter.close(), false);
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
