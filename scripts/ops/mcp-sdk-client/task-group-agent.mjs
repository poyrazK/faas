import { once } from 'node:events';
import { Client, StreamableHTTPClientTransport } from '@modelcontextprotocol/client';
import { createMcpTaskTransport, watchMcpTaskGroup } from '../../../sdk/node/dist/preview.js';

const [configuration] = await once(process, 'message');
const { endpoint, token, group } = configuration;
const client = new Client({ name: 'gregale-task-group-restart', version: '1.0.0' }, {
  versionNegotiation: { mode: { pin: '2026-07-28' } }, enforceStrictCapabilities: true,
});

try {
  await client.connect(createMcpTaskTransport(StreamableHTTPClientTransport, endpoint, {
    requestInit: { headers: { Authorization: `Bearer ${token}` } }, onInsufficientScope: 'throw',
  }));
  for await (const event of watchMcpTaskGroup(client, group, { timeoutMs: 8_000 })) {
    if (event.outcome === 'unavailable') throw event.error;
    await event.acknowledge();
    process.send({
      receiptIndex: event.receiptIndex, taskId: event.task.taskId, status: event.task.status, updateId: event.updateId,
    });
  }
} catch (error) {
  console.error('Task group agent failed', error?.code ?? error?.name);
  process.exitCode = 1;
} finally {
  await client.close();
  process.disconnect();
}
