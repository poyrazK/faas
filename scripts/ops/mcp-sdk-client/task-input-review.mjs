import assert from 'node:assert/strict';
import { randomBytes, randomUUID } from 'node:crypto';

import { Client, StreamableHTTPClientTransport } from '@modelcontextprotocol/client';
import {
  createMcpTaskReviewFetch, createMcpTaskSubmissionFetch, getMcpTask, getTaskInputReview, listTaskInbox,
  lookupMcpTaskBySubmissionKey, McpTaskReviewNumber, respondToReviewedTaskInput, submitMcpTask, waitForMcpTask,
} from '../../../sdk/node/dist/preview.js';

const [endpoint, taskId, racingId] = process.argv.slice(2);
const signal = AbortSignal.timeout(15_000);
const options = { signal };

async function connect({ keyed = true } = {}) {
  const client = new Client({ name: 'gregale-review-sdk-interop', version: '1.0.0' }, {
    versionNegotiation: { mode: { pin: '2026-07-28' } }, enforceStrictCapabilities: true,
  });
  const reviewFetch = createMcpTaskReviewFetch();
  await client.connect(new StreamableHTTPClientTransport(new URL(endpoint), {
    requestInit: { headers: { Authorization: `Bearer ${process.env.MCP_TASKS_TEST_TOKEN}` } },
    onInsufficientScope: 'throw',
    fetch: keyed ? createMcpTaskSubmissionFetch(reviewFetch) : reviewFetch,
  }));
  return client;
}

const client = await connect();
try {
  const inbox = await listTaskInbox(client, { status: 'input_required', signal });
  assert.deepEqual(new Set(inbox.tasks.map(entry => entry.taskId)), new Set([taskId, racingId]));

  const review = await getTaskInputReview(client, taskId, options);
  assert.equal(review.task.nextAction, 'provide_input');
  assert.equal(review.forms.approval.message, 'Approve generating this report?');
  const amount = review.forms.approval.schema.properties.amount.const;
  assert.ok(amount instanceof McpTaskReviewNumber);
  assert.equal(amount.source, '900719925474099312345');
  const rate = new McpTaskReviewNumber('0.1000000000000000001');
  const answers = { approval: { action: 'accept', content: { approved: true, amount, rate } } };
  const lostBeforeCommit = await respondToReviewedTaskInput(client, review, answers, options);
  assert.equal(lostBeforeCommit.outcome, 'unconfirmed');
  assert.equal(lostBeforeCommit.task.status, 'input_required');

  const lostAfterCommit = await respondToReviewedTaskInput(client, review, answers, options);
  assert.equal(lostAfterCommit.outcome, 'applied');
  const paused = await waitForMcpTask(client, racingId, options);
  assert.equal(paused.status, 'input_required');
  assert.equal(paused.nextAction, 'provide_input');
  assert.equal(Object.hasOwn(paused, 'inputRequests'), false);

  const racing = await getTaskInputReview(client, racingId, options);
  assert.equal((await respondToReviewedTaskInput(client, racing, {
    approval: { action: 'accept', content: { approved: true } },
  }, options)).outcome, 'stale');
} finally {
  await client.close();
}

function newSubmissionKey() {
  const bytes = randomBytes(16);
  bytes.writeUIntBE(Date.now() - 1000, 0, 6);
  bytes[6] = (bytes[6] & 0x0f) | 0x70;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;
  const hex = bytes.toString('hex');
  return [hex.slice(0, 8), hex.slice(8, 12), hex.slice(12, 16), hex.slice(16, 20), hex.slice(20)].join('-');
}

const submission = { toolName: 'build_report', arguments: { report: 'keyed SDK journey', steps: 1 } };

// Attach a new client to the same Tasks with only their IDs.
const resumed = await connect();
try {
  const finished = await waitForMcpTask(resumed, taskId, options);
  assert.equal(finished.taskId, taskId);
  assert.equal(finished.status, 'completed');
  assert.match(JSON.stringify(finished.result), /Report SDK journey is ready/);

  const reread = await getMcpTask(resumed, taskId, options);
  assert.equal(reread.status, 'completed');
  assert.deepEqual(reread.result, finished.result);
  await assert.rejects(getMcpTask(resumed, randomUUID(), options), { code: 'not_found' });

  const completed = await getTaskInputReview(resumed, taskId, options);
  assert.equal(completed.task.status, 'completed');
  assert.deepEqual(completed.forms, {});

  const declined = await waitForMcpTask(resumed, racingId, options);
  assert.equal(declined.status, 'completed');
  assert.match(JSON.stringify(declined.result), /Report was not approved/);

  // Keep the key before sending. The server loses the first reply after commit.
  submission.submissionKey = newSubmissionKey();
  assert.deepEqual(await submitMcpTask(resumed, submission, options), { outcome: 'unconfirmed' });
} finally {
  await resumed.close();
}

const recovered = await connect();
try {
  const found = await lookupMcpTaskBySubmissionKey(recovered, submission.submissionKey, options);
  assert.equal(Object.hasOwn(found, 'result'), false);
  const replay = await submitMcpTask(recovered, submission, options);
  assert.equal(replay.outcome, 'accepted');
  assert.equal(replay.task.taskId, found.taskId);
  assert.equal(replay.task.createdAt, found.createdAt);
  assert.equal(replay.task.ttlMs, found.ttlMs);
  await assert.rejects(submitMcpTask(recovered, {
    ...submission, arguments: { report: 'changed', steps: 1 },
  }, options), { code: 'rejected' });
  const finished = await waitForMcpTask(recovered, found.taskId, options);
  assert.equal(finished.status, 'completed');
  assert.deepEqual(finished.result.content, [{ type: 'text', text: 'Report keyed SDK journey is ready after 1 steps.' }]);
} finally {
  await recovered.close();
}

const unadapted = await connect({ keyed: false });
try {
  await assert.rejects(submitMcpTask(unadapted, { ...submission, submissionKey: newSubmissionKey() }, options),
    { code: 'rejected' });
} finally {
  await unadapted.close();
}
