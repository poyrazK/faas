import { parseRecord } from '../../../cmd/gregale/templates/agent-stateless-node/agent.js';

const respond = (statusCode, body) => ({ statusCode, headers: { 'content-type': 'application/json' }, body: JSON.stringify(body) });

export function createHostedReportHandler({ endpointID, processRecord }) {
  if (!endpointID) throw new Error('Configure REPORT_ENDPOINT_ID');
  return async function handler(event) {
    const path = String(event?.path ?? '').split('?')[0];
    if (path === '/realtime/connect') return respond(403, { error: 'inbox accepts backend publication only' });
    // The gateway strips customer-supplied invocation source headers.
    const source = Object.entries(event?.headers ?? {}).find(([key]) => key.toLowerCase() === 'x-faas-invocation-source')?.[1];
    if (event?.method !== 'POST' || String(source ?? '') !== 'esm' || !path.startsWith('/_triggers/esm/')) {
      return respond(404, { error: 'not found' });
    }
    let record;
    try {
      record = typeof event.body === 'string' ? JSON.parse(event.body) : event.body;
    } catch {
      return respond(400, { error: 'invalid inbox record' });
    }
    if (record?.endpoint_id !== endpointID || record.channel !== 'agent-inbox' || record.group !== 'slices' || !parseRecord(record)) {
      return respond(400, { error: 'invalid report inbox record' });
    }
    // Rejected processing remains retryable through the realtime consumer group.
    await processRecord(record);
    return respond(200, { ok: true });
  };
}
