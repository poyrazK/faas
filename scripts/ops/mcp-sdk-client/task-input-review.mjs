import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';

import { Client, StreamableHTTPClientTransport } from '@modelcontextprotocol/client';
import {
  createMcpTaskReviewFetch, getMcpTask, getTaskInputReview, listTaskInbox,
  McpTaskReviewNumber, respondToReviewedTaskInput, waitForMcpTask,
} from '../../../sdk/node/dist/preview.js';

const [endpoint, taskId, racingId] = process.argv.slice(2);
const signal = AbortSignal.timeout(15_000);
const options = { signal };

async function connect() {
  const client = new Client({ name: 'gregale-review-sdk-interop', version: '1.0.0' }, {
    versionNegotiation: { mode: { pin: '2026-07-28' } }, enforceStrictCapabilities: true,
  });
  await client.connect(new StreamableHTTPClientTransport(new URL(endpoint), {
    requestInit: { headers: { Authorization: `Bearer ${process.env.MCP_TASKS_TEST_TOKEN}` } },
    onInsufficientScope: 'throw',
    fetch: createMcpTaskReviewFetch(),
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
} finally {
  await resumed.close();
}
