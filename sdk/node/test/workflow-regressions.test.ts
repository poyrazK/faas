import test from 'node:test';
import assert from 'node:assert/strict';
import {
  defineWorkflow, NonRetryableError, runWorkflowInvocation,
  WORKFLOW_CODE_PROTOCOL, WORKFLOW_RESPONSE_MAX_BYTES,
} from '../src/workflows.js';

const request = (journal: unknown[] = []) => ({ protocol: WORKFLOW_CODE_PROTOCOL, run_id: 'run', workflow: 'regression', input: {}, invocation: 1, journal });

test('large nonretryable errors fit in the workflow response', async () => {
  const definition = defineWorkflow('regression', async (_input, wf) => wf.step('work', () => {
    throw new NonRetryableError('x'.repeat(2 * 1024 * 1024));
  }));
  const response = await runWorkflowInvocation(definition, request());
  assert.equal(response.ops[0]?.non_retryable, true);
  assert.ok(Buffer.byteLength(JSON.stringify(response)) <= WORKFLOW_RESPONSE_MAX_BYTES);
});

test('terminal responses report how much history was replayed', async () => {
  const definition = defineWorkflow('regression', async () => 'done');
  const response = await runWorkflowInvocation(definition, request([{ seq: 0, name: 'approval', kind: 'event', status: 'waiting' }]));
  assert.equal(response.replayed_ops, 0);
});

test('large terminal errors fit in the workflow response', async () => {
  const definition = defineWorkflow('regression', async () => { throw new Error('failure'.repeat(300000)); });
  const response = await runWorkflowInvocation(definition, request());
  assert.equal(response.result.status, 'failed');
  assert.ok(Buffer.byteLength(JSON.stringify(response)) <= WORKFLOW_RESPONSE_MAX_BYTES);
});

test('parallel errors share one response budget', async () => {
  const definition = defineWorkflow('regression', async (_input, wf) => Promise.all(Array.from({ length: 1000 }, (_, i) => wf.step(`work-${i}`, () => {
    throw new NonRetryableError('failure'.repeat(1000));
  }))));
  const response = await runWorkflowInvocation(definition, request());
  assert.equal(response.ops.length, 1000);
  assert.ok(Buffer.byteLength(JSON.stringify(response)) <= WORKFLOW_RESPONSE_MAX_BYTES);
});

test('maximum length base names retain the canonical repeat suffix', async () => {
  const name = 'a'.repeat(128);
  let calls = 0;
  const definition = defineWorkflow('regression', async (_input, wf) => {
    for (let i = 0; i < 2; i++) await wf.step(name, () => ++calls);
  });
  const response = await runWorkflowInvocation(definition, request([{ seq: 0, name, kind: 'step', status: 'succeeded', attempt: 1, output: 1 }]));
  assert.equal(response.ops[0]?.name, `${name}#2`);
  assert.equal(calls, 1);
  assert.equal(response.replayed_ops, 1);
});
