import { once } from 'node:events';
import { Client, StreamableHTTPClientTransport } from '@modelcontextprotocol/client';
import {
  continueMcpTaskGroupSubmission, createMcpTaskTransport, getMcpTaskGroupResults, resumeMcpTaskGroupSubmission,
  stopMcpTaskGroup, submitMcpTaskGroup, watchMcpTaskGroup,
} from '../../../sdk/node/dist/preview.js';

const [configuration] = await once(process, 'message');
const { endpoint, token, group, followInput, submissions, continuation, stopGroup, collectResults, keyAgeMs = 0 } = configuration;
const client = new Client({ name: 'gregale-task-group-restart', version: '1.0.0' }, {
  versionNegotiation: { mode: { pin: '2026-07-28' } }, enforceStrictCapabilities: true,
});

try {
  await client.connect(createMcpTaskTransport(StreamableHTTPClientTransport, endpoint, {
    requestInit: { headers: { Authorization: `Bearer ${token}` } }, onInsufficientScope: 'throw',
  }));
  if (collectResults) {
    process.send({ results: await getMcpTaskGroupResults(client, group) });
  } else if (stopGroup) {
    process.send({ stopped: await stopMcpTaskGroup(client, group) });
  } else if (submissions) {
    const realNow = Date.now;
    // Start near the end of the server's real admission window so the
    // integration test can cross it without waiting a whole minute.
    Date.now = () => realNow() - keyAgeMs;
    try {
      process.send({ submitted: await submitMcpTaskGroup(client, group, submissions) });
    } finally {
      Date.now = realNow;
    }
  } else if (continuation) {
    process.send({ recovered: await resumeMcpTaskGroupSubmission(client, group) });
    const [action] = await once(process, 'message');
    if (action !== 'continue') throw new Error('Unexpected Task group action');
    const { receiptIndex, submission } = continuation;
    process.send({ continued: await continueMcpTaskGroupSubmission(client, group, receiptIndex, submission) });
  } else {
    for await (const event of watchMcpTaskGroup(client, group, { timeoutMs: 8_000, followInput })) {
      if (event.outcome === 'unavailable') throw event.error;
      await event.acknowledge();
      process.send({
        receiptIndex: event.receiptIndex, taskId: event.task.taskId, status: event.task.status, updateId: event.updateId,
      });
    }
  }
} catch (error) {
  console.error('Task group agent failed', error?.code ?? error?.name);
  process.exitCode = 1;
} finally {
  await client.close();
  process.disconnect();
}
