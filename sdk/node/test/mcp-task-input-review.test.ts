import assert from 'node:assert/strict';
import test from 'node:test';

import {
  createMcpTaskReviewFetch, getTaskInputReview, McpTaskReviewNumber, respondToReviewedTaskInput,
  type McpTaskInputReviewConnection, type McpTaskReviewAnswers,
} from '../src/preview.js';

const timestamp = '2026-11-24T01:00:00.123456789Z';
const task = { resultType: 'complete', taskId: 'pending', status: 'input_required', createdAt: timestamp,
  lastUpdatedAt: timestamp, ttlMs: 60_000, pollIntervalMs: 500 };
const revision = 'a'.repeat(64);
const requestedSchema = { type: 'object', properties: { approved: { type: 'boolean' } } };
const form = { method: 'elicitation/create', params: { message: 'Approve?', requestedSchema } };
const pending = { task, reviewRevision: revision, inputRequests: { approval: form } };
const accept = { approval: { action: 'accept', content: { approved: true } } } as const;
const read = 'gregale/tasks/input-review';
const write = 'gregale/tasks/respond-reviewed';
const check = 'gregale/tasks/check-reviewed-response';

function connection(request: McpTaskInputReviewConnection['request']): McpTaskInputReviewConnection {
  return {
    getNegotiatedProtocolVersion: () => '2026-07-28',
    getServerCapabilities: () => ({ extensions: { 'io.modelcontextprotocol/tasks': {}, 'io.gregale/task-input-review': {} } }),
    request,
  };
}

test('review projects metadata and freezes the exact form snapshot', async () => {
  const client = connection(async (request, _, options) => {
    assert.deepEqual(request, { method: read, params: { taskId: 'pending' } });
    assert.equal(options.allowInputRequired, true);
    assert.equal(options.timeout, 2_000);
    return { ...pending, unrelated: 'private fixture' };
  });
  const review = await getTaskInputReview(client, 'pending', { timeoutMs: 2_000 });
  assert.deepEqual(review, { task: { taskId: 'pending', status: 'input_required', createdAt: timestamp,
    lastUpdatedAt: timestamp, ttlMs: 60_000, pollIntervalMs: 500, nextAction: 'provide_input' },
    reviewRevision: revision, forms: { approval: { message: 'Approve?', schema: requestedSchema } } });
  for (const value of [review, review.task, review.forms, review.forms.approval, review.forms.approval.schema.properties]) {
    assert.ok(Object.isFrozen(value));
  }

  const complete = connection(async () => ({ task: { ...task, status: 'completed' } }));
  const completed = await getTaskInputReview(complete, task.taskId);
  assert.equal(completed.reviewRevision, undefined);
  assert.deepEqual(completed.forms, {});
  await assert.rejects(respondToReviewedTaskInput(complete, completed, {}), { code: 'invalid_review' });
});

test('review rejects unsupported connections, mismatched Tasks and inexact or unsupported forms', async () => {
  const client = connection(async () => assert.fail('unsupported review was requested'));
  await assert.rejects(getTaskInputReview({ ...client, getServerCapabilities: () => ({}) }, task.taskId),
    { code: 'unavailable' });
  await assert.rejects(getTaskInputReview(client, 'invalid\n'), { code: 'invalid_review' });
  for (const value of [
    { ...pending, reviewRevision: revision + '\n' },
    { ...pending, task: { ...task, taskId: 'different' } },
    { ...pending, inputRequests: { approval: { ...form, params: { ...form.params, mode: 'url' } } } },
    { ...pending, inputRequests: { approval: { ...form, params: {
      requestedSchema: { ...requestedSchema, description: 'x'.repeat(64 << 10) },
    } } } },
  ]) {
    await assert.rejects(getTaskInputReview(connection(async () => value), task.taskId), { code: 'invalid_review' });
  }
  const numeric = { ...form, params: { ...form.params, requestedSchema: { ...requestedSchema, minimum: 1 } } };
  await assert.rejects(getTaskInputReview(connection(async () => ({ ...pending, inputRequests: { approval: numeric } })),
    task.taskId), { code: 'exact_transport_required' });
});

test('the review transport preserves numeric literals and leaves other responses alone', async () => {
  const body = '{"id":7,"result":{"count":1,"inputRequests":{"approval":{"params":{"requestedSchema":' +
    '{"const":0.1000000000000000001,"enum":[900719925474099312345,1e400,-0]}}}}}}';
  const exactFetch = createMcpTaskReviewFetch(async () => new Response(body, {
    headers: { 'Content-Type': 'application/json; charset=utf-8' },
  }));
  const response = await exactFetch(new Request('https://mcp.example/mcp', {
    method: 'POST', headers: { 'Mcp-Method': read },
  }));
  const parsed = await response.json();
  const schema = parsed.result.inputRequests.approval.params.requestedSchema;
  assert.equal(parsed.id, 7);
  assert.equal(parsed.result.count, 1);
  assert.ok(schema.const instanceof McpTaskReviewNumber);
  assert.equal(schema.const.source, '0.1000000000000000001');
  assert.deepEqual(schema.enum.map(String), ['900719925474099312345', '1e400', '-0']);
  assert.equal(JSON.stringify(schema), '{"const":0.1000000000000000001,"enum":[900719925474099312345,1e400,-0]}');

  const other = await exactFetch('https://mcp.example/mcp', { method: 'POST', headers: { 'Mcp-Method': check } });
  assert.equal((await other.json()).result.inputRequests.approval.params.requestedSchema.const, 0.1);
  for (const literal of ['01', '1\n', '+1', 'Infinity', 'null']) {
    assert.throws(() => new McpTaskReviewNumber(literal), TypeError);
  }
});

test('definite reviewed outcomes require one write and no confirmation', async () => {
  for (const outcome of ['applied', 'stale'] as const) {
    const calls: string[] = [];
    const client = connection(async ({ method, params }) => {
      calls.push(method);
      if (method === read) return pending;
      assert.deepEqual(params, { taskId: task.taskId, reviewRevision: revision, responses: accept });
      return { task, outcome };
    });
    assert.equal((await respondToReviewedTaskInput(client, await getTaskInputReview(client, task.taskId), accept)).outcome, outcome);
    assert.deepEqual(calls, [read, write]);
  }
});

test('one read-only check uses the same immutable answers after a lost reply', async () => {
  for (const outcome of ['applied', 'unconfirmed'] as const) {
    const calls: string[] = [];
    const answers = { approval: { action: 'accept' as const, content: { approved: true, values: [new McpTaskReviewNumber('900719925474099312345')] } } };
    let submitted: unknown;
    const client = connection(async ({ method, params }) => {
      calls.push(method);
      if (method === read) return pending;
      if (method === write) {
        submitted = params.responses;
        answers.approval.content.values.pop();
        throw new TypeError('lost reply');
      }
      assert.equal(params.responses, submitted);
      assert.equal(JSON.stringify(params.responses), '{"approval":{"action":"accept","content":{"approved":true,"values":[900719925474099312345]}}}');
      return { task, outcome };
    });
    assert.equal((await respondToReviewedTaskInput(client, await getTaskInputReview(client, task.taskId), answers)).outcome, outcome);
    assert.deepEqual(calls, [read, write, check]);
  }
});

test('reviewed input rejects foreign snapshots and malformed answers before transmitting', async () => {
  const client = connection(async ({ method }) => {
    assert.equal(method, read);
    return pending;
  });
  const review = await getTaskInputReview(client, task.taskId);
  await assert.rejects(respondToReviewedTaskInput({ ...client }, review, accept), { code: 'invalid_review' });
  await assert.rejects(respondToReviewedTaskInput(client, { ...review }, accept), { code: 'invalid_review' });
  const invalid: McpTaskReviewAnswers[] = [{}, { other: accept.approval },
    { approval: { action: 'accept', content: { unsafe: 2 ** 53 } } } as const,
    { approval: { action: 'accept', content: { tooLarge: 'x'.repeat(256 << 10) } } }];
  for (const answers of invalid) {
    await assert.rejects(respondToReviewedTaskInput(client, review, answers), { code: 'invalid_answers' });
  }
});

test('definite authorization errors are sanitized and cancellation after transmission stays unconfirmed', async () => {
  const denied = connection(async ({ method }) => {
    if (method === read) return pending;
    assert.equal(method, write);
    throw Object.assign(new Error('private body'), { status: 403 });
  });
  await assert.rejects(respondToReviewedTaskInput(denied, await getTaskInputReview(denied, task.taskId), accept), {
    code: 'request_failed', message: 'MCP Task input review request failed', status: 403,
  });
  const controller = new AbortController();
  const aborting = connection(async ({ method }) => {
    if (method === read) return pending;
    assert.equal(method, write);
    controller.abort();
    throw controller.signal.reason;
  });
  assert.deepEqual(await respondToReviewedTaskInput(aborting, await getTaskInputReview(aborting, task.taskId), accept,
    { signal: controller.signal }), { outcome: 'unconfirmed' });
});

test('an invalid or unavailable confirmation leaves the original write unconfirmed', async () => {
  for (const reply of [{}, { task: { ...task, taskId: 'other' }, outcome: 'applied' }, undefined]) {
    const calls: string[] = [];
    const client = connection(async ({ method }) => {
      calls.push(method);
      if (method === read) return pending;
      if (method === write) throw Object.assign(new Error('internal error after write'), { code: -32603 });
      if (reply === undefined) throw new Error('confirmation is unavailable');
      return reply;
    });
    assert.deepEqual(await respondToReviewedTaskInput(client, await getTaskInputReview(client, task.taskId), accept),
      { outcome: 'unconfirmed' });
    assert.deepEqual(calls, [read, write, check]);
  }
});
