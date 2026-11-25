import assert from 'node:assert/strict';

import { Client, StreamableHTTPClientTransport } from '@modelcontextprotocol/client';
import {
  createMcpTaskReceiptTransport, resumeMcpTaskFromReceipt, retryMcpTaskFromReceipt,
  submitMcpTaskWithReceipt, waitForMcpTask,
} from '../../../sdk/node/dist/preview.js';

const [mode, endpoint, receiptPath] = process.argv.slice(2);
const client = new Client({ name: 'gregale-task-receipt-interop', version: '1.0.0' }, {
  versionNegotiation: { mode: { pin: '2026-07-28' } }, enforceStrictCapabilities: true,
});
await client.connect(createMcpTaskReceiptTransport(StreamableHTTPClientTransport, endpoint, {
  requestInit: { headers: { Authorization: `Bearer ${process.env.MCP_TASKS_TEST_TOKEN}` } },
  onInsufficientScope: 'throw',
}));
const options = { signal: AbortSignal.timeout(12_000) };
const submission = { toolName: 'build_report', arguments: { report: 'crash receipt journey', steps: 1 } };

try {
  if (mode === 'submit') {
    await submitMcpTaskWithReceipt(client, receiptPath, submission, options);
    assert.fail('receipt journey did not kill the submitting process before its reply');
  }
  if (mode === 'reject-credential') {
    await assert.rejects(retryMcpTaskFromReceipt(client, receiptPath, submission, options), { code: 'credential_mismatch' });
  } else {
    let recovered;
    if (mode === 'retry') {
      const retried = await retryMcpTaskFromReceipt(client, receiptPath, submission, options);
      assert.equal(retried.outcome, 'accepted');
      assert.equal(retried.taskIdRecorded, true);
      recovered = await waitForMcpTask(client, retried.task.taskId, options);
    } else {
      assert.equal(mode, 'resume');
      recovered = await resumeMcpTaskFromReceipt(client, receiptPath, options);
    }
    const result = recovered.status === 'working' ? await waitForMcpTask(client, recovered.taskId, options) : recovered;
    assert.equal(result.status, 'completed');
    assert.deepEqual(result.result.content, [{ type: 'text', text: 'Report crash receipt journey is ready after 1 steps.' }]);
    console.log(JSON.stringify({ taskId: result.taskId, status: result.status }));
  }
} finally {
  await client.close();
}
