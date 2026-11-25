import assert from 'node:assert/strict';

import { Client, StreamableHTTPClientTransport } from '@modelcontextprotocol/client';
import {
  createMcpTaskReceiptTransport, resumeMcpTaskFromReceipt, submitMcpTaskWithReceipt, waitForMcpTask,
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

try {
  if (mode === 'submit') {
    await submitMcpTaskWithReceipt(client, receiptPath, {
      toolName: 'build_report', arguments: { report: 'crash receipt journey', steps: 1 },
    }, options);
    assert.fail('receipt journey did not kill the submitting process before its reply');
  }
  assert.equal(mode, 'resume');
  const recovered = await resumeMcpTaskFromReceipt(client, receiptPath, options);
  const result = recovered.status === 'working' ? await waitForMcpTask(client, recovered.taskId, options) : recovered;
  assert.equal(result.status, 'completed');
  assert.deepEqual(result.result.content, [{ type: 'text', text: 'Report crash receipt journey is ready after 1 steps.' }]);
  console.log(JSON.stringify({ taskId: result.taskId, status: result.status }));
} finally {
  await client.close();
}
