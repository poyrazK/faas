import assert from 'node:assert/strict';
import { setTimeout as delay } from 'node:timers/promises';

import { Client, StreamableHTTPClientTransport } from '@modelcontextprotocol/client';
import {
  createMcpTaskReviewFetch, getTaskInputReview, listTaskInbox, McpTaskReviewNumber, respondToReviewedTaskInput,
} from '../../../sdk/node/dist/preview.js';

const [endpoint, taskId, racingId] = process.argv.slice(2);
const client = new Client({ name: 'gregale-review-sdk-interop', version: '1.0.0' }, {
  versionNegotiation: { mode: { pin: '2026-07-28' } }, enforceStrictCapabilities: true,
});
const signal = AbortSignal.timeout(15_000);
const options = { signal };

async function originalResult(id) {
  const schema = { '~standard': { version: 1, vendor: 'fixture', validate: value => ({ value }) } };
  for (;;) {
    const result = await client.request({ method: 'tasks/get', params: { taskId: id } }, schema, {
      signal, allowInputRequired: true,
    });
    if (result.status === 'completed') return result.result;
    assert.ok(['working', 'input_required'].includes(result.status), 'Task had an unexpected status');
    await delay(100, undefined, { signal });
  }
}

try {
  await client.connect(new StreamableHTTPClientTransport(new URL(endpoint), {
    requestInit: { headers: { Authorization: `Bearer ${process.env.MCP_TASKS_TEST_TOKEN}` } },
    onInsufficientScope: 'throw',
    fetch: createMcpTaskReviewFetch(),
  }));
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
  assert.match(JSON.stringify(await originalResult(taskId)), /Report SDK journey is ready/);
  const completed = await getTaskInputReview(client, taskId, options);
  assert.equal(completed.task.status, 'completed');
  assert.deepEqual(completed.forms, {});

  const racing = await getTaskInputReview(client, racingId, options);
  assert.equal((await respondToReviewedTaskInput(client, racing, {
    approval: { action: 'accept', content: { approved: true } },
  }, options)).outcome, 'stale');
  assert.match(JSON.stringify(await originalResult(racingId)), /Report was not approved/);
} finally {
  await client.close();
}
