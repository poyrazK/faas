import test from 'node:test';
import assert from 'node:assert/strict';
import {
  defineWorkflow, NonRetryableError, parseDuration, runWorkflowInvocation, runWorkflowLocally, serveWorkflows,
  WORKFLOW_CODE_PROTOCOL, WORKFLOW_RESPONSE_MAX_BYTES, WorkflowStepError, type JournalEntry,
} from '../src/workflows.js';

const request = (workflow: string, journal: JournalEntry[], input: unknown = {}) => ({
  protocol: WORKFLOW_CODE_PROTOCOL, run_id: 'run-1', workflow, input, invocation: journal.length + 1, journal,
});

const order = defineWorkflow('order', async (input: { id: string }, wf) => {
  const charge = await wf.step('charge', ({ idempotencyKey }) => ({ id: `ch_${input.id}`, key: idempotencyKey }));
  await wf.sleep('cool_off', '3d');
  const approval = await wf.waitForEvent<{ ok: boolean }>('approval', { event: 'approved', timeout: '7d' });
  if (!approval) return { status: 'expired', charge: charge.id };
  return { status: approval.ok ? 'shipped' : 'refused', charge: charge.id };
});

test('the first call runs the first step and suspends', async () => {
  const response = await runWorkflowInvocation([order], request('order', [], { id: '42' }));
  assert.equal(response.protocol, WORKFLOW_CODE_PROTOCOL);
  assert.deepEqual(response.result, { status: 'running' });
  assert.deepEqual(response.ops, [{ seq: 0, name: 'charge', kind: 'step', status: 'succeeded', output: { id: 'ch_42', key: 'workflow/run-1/charge' } }]);
});

test('recorded steps replay without running and waits are reported once', async () => {
  let charged = 0;
  const counted = defineWorkflow('counted', async (_input, wf) => {
    await wf.step('charge', () => { charged++; return 1; });
    await wf.sleep('cool_off', 5_000);
    return 'done';
  });
  const journal: JournalEntry[] = [{ seq: 0, name: 'charge', kind: 'step', status: 'succeeded', attempt: 1, output: 1 }];
  const response = await runWorkflowInvocation(counted, request('counted', journal));
  assert.equal(charged, 0);
  assert.deepEqual(response.ops, [{ seq: 1, name: 'cool_off', kind: 'sleep', duration_ms: 5_000 }]);
  const waiting = await runWorkflowInvocation(counted, request('counted', [...journal, { seq: 1, name: 'cool_off', kind: 'sleep', status: 'waiting' }]));
  assert.deepEqual(waiting.ops, []);
  assert.equal(waiting.result.status, 'running');
  const done = await runWorkflowInvocation(counted, request('counted', [...journal, { seq: 1, name: 'cool_off', kind: 'sleep', status: 'succeeded' }]));
  assert.deepEqual(done.result, { status: 'succeeded', output: 'done' });
});

test('parallel steps run in one call and report in sequence order', async () => {
  const fanout = defineWorkflow('fanout', async (_input, wf) => {
    const [a, b] = await Promise.all([
      wf.step('slow', () => new Promise((resolve) => setTimeout(() => resolve('a'), 20))),
      wf.step('fast', () => 'b'),
    ]);
    return `${a}${b}`;
  });
  const first = await runWorkflowInvocation(fanout, request('fanout', []));
  assert.deepEqual(first.ops.map((op) => [op.seq, op.name, op.output]), [[0, 'slow', 'a'], [1, 'fast', 'b']]);
  const journal = first.ops.map((op): JournalEntry => ({ seq: op.seq, name: op.name, kind: 'step', status: 'succeeded', attempt: 1, output: op.output }));
  assert.deepEqual((await runWorkflowInvocation(fanout, request('fanout', journal))).result, { status: 'succeeded', output: 'ab' });
});

test('repeated names get stable suffixes', async () => {
  const loop = defineWorkflow('loop', async (_input, wf) => {
    for (let i = 0; i < 3; i++) await wf.step('poll', () => i);
    return 'ok';
  });
  const result = await runWorkflowLocally(loop, {});
  assert.equal(result.status, 'succeeded');
  assert.deepEqual(result.journal.map((entry) => entry.name), ['poll', 'poll#2', 'poll#3']);
});

test('a code change at a recorded position is reported for the engine to refuse', async () => {
  const changed = defineWorkflow('order', async (_input, wf) => {
    await wf.step('charge_v2', () => 1);
    return 'never';
  });
  const response = await runWorkflowInvocation(changed, request('order', [{ seq: 0, name: 'charge', kind: 'step', status: 'succeeded', attempt: 1, output: 1 }]));
  assert.deepEqual(response.ops, [{ seq: 0, name: 'charge_v2', kind: 'step', status: 'succeeded' }]);
  assert.equal(response.result.status, 'running');
});

test('failures carry policy, retry on the next call and finally throw into the workflow', async () => {
  const flaky = defineWorkflow('flaky', async (_input, wf) => {
    try {
      await wf.step('call', ({ attempt }) => { throw new Error(`boom ${attempt}`); }, { retry: { maxAttempts: 2, backoff: 'exponential' } });
    } catch (error) {
      assert.ok(error instanceof WorkflowStepError);
      return { recovered: error.message, attempts: error.attempts };
    }
    return 'unreachable';
  });
  const first = await runWorkflowInvocation(flaky, request('flaky', []));
  assert.deepEqual(first.ops, [{ seq: 0, name: 'call', kind: 'step', status: 'failed', error: 'boom 1', retry: { max_attempts: 2, backoff: 'exponential' } }]);
  const second = await runWorkflowInvocation(flaky, request('flaky', [{ seq: 0, name: 'call', kind: 'step', status: 'retrying', attempt: 1, error: 'boom 1' }]));
  assert.equal(second.ops[0]?.error, 'boom 2');
  const local = await runWorkflowLocally(flaky, {});
  assert.deepEqual(local.output, { recovered: 'boom 2', attempts: 2 });
});

test('non-retryable and non-JSON results fail without retry', async () => {
  const strict = defineWorkflow('strict', async (_input, wf) => {
    await wf.step('reject', () => { throw new NonRetryableError('card declined'); });
  });
  const response = await runWorkflowInvocation(strict, request('strict', []));
  assert.equal(response.ops[0]?.non_retryable, true);
  const bigint = defineWorkflow('bigint', async (_input, wf) => wf.step('value', () => 1n));
  const failed = await runWorkflowInvocation(bigint, request('bigint', []));
  assert.equal(failed.ops[0]?.status, 'failed');
  assert.equal(failed.ops[0]?.non_retryable, true);
});

test('events resolve in order and time out to null', async () => {
  const approved = await runWorkflowLocally(order, { id: '7' }, { events: { approved: [{ ok: true }] } });
  assert.deepEqual(approved.output, { status: 'shipped', charge: 'ch_7' });
  const expired = await runWorkflowLocally(order, { id: '7' });
  assert.deepEqual(expired.output, { status: 'expired', charge: 'ch_7' });
  const first = await runWorkflowInvocation(order, request('order', [
    { seq: 0, name: 'charge', kind: 'step', status: 'succeeded', attempt: 1, output: { id: 'ch_7' } },
    { seq: 1, name: 'cool_off', kind: 'sleep', status: 'succeeded' },
  ]));
  assert.deepEqual(first.ops, [{ seq: 2, name: 'approval', kind: 'event', event: 'approved', timeout_ms: 7 * 86_400_000 }]);
});

test('a thrown error outside steps fails the workflow', async () => {
  const broken = defineWorkflow('broken', async () => { throw new Error('bad input'); });
  assert.deepEqual((await runWorkflowInvocation(broken, request('broken', []))).result, { status: 'failed', error: 'bad input' });
});

test('serveWorkflows only accepts Gregale workflow calls', async () => {
  const handler = serveWorkflows([order]);
  const body = JSON.stringify(request('order', [], { id: '1' }));
  const refused = await handler(new Request('http://app/order', { method: 'POST', body }));
  assert.equal(refused.status, 403);
  const accepted = await handler(new Request('http://app/order', { method: 'POST', body, headers: { 'x-faas-workflow-mode': 'code', 'x-faas-workflow-run-id': 'run-1' } }));
  assert.equal(accepted.status, 200);
  assert.equal((await accepted.json() as { ops: unknown[] }).ops.length, 1);
  const unknown = await serveWorkflows([order], { allowUnverifiedCallers: true })(new Request('http://app/x', { method: 'POST', body: JSON.stringify(request('missing', [])) }));
  assert.equal(unknown.status, 404);
  assert.equal((await handler(new Request('http://app/order'))).status, 405);
});

test('results that overflow one answer fail without retry', async () => {
  const big = 'x'.repeat(600 * 1024);
  const huge = defineWorkflow('huge', async (_input, wf) => Promise.all([wf.step('a', () => big), wf.step('b', () => big)]));
  const response = await runWorkflowInvocation(huge, request('huge', []));
  assert.equal(response.ops[0]?.status, 'succeeded');
  assert.equal(response.ops[1]?.status, 'failed');
  assert.equal(response.ops[1]?.non_retryable, true);
  assert.equal(response.ops[1]?.output, undefined);
  assert.ok(Buffer.byteLength(JSON.stringify(response)) <= WORKFLOW_RESPONSE_MAX_BYTES);
});

test('parseDuration accepts milliseconds and unit strings', () => {
  assert.equal(parseDuration(1500), 1500);
  assert.equal(parseDuration('30s'), 30_000);
  assert.equal(parseDuration('1.5h'), 5_400_000);
  assert.equal(parseDuration('3d'), 259_200_000);
  assert.throws(() => parseDuration('soon'));
  assert.throws(() => parseDuration(-1));
});
