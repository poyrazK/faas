// Invoked once per persisted inbox record by the real-KVM qualification.
import { readFileSync, writeSync } from 'node:fs';
import { PostgresStore } from '../../../cmd/gregale/templates/agent-stateless-node/store.js';
import { GregalePlatform } from '../../../cmd/gregale/templates/agent-stateless-node/platform.js';
import { createReportAgent } from '../agent/agent.mjs';
import { buildExamples, jsonFile } from '../scripts/manifests.mjs';

const [mode, endpointID] = process.argv.slice(2);
if (mode === 'fixtures') {
  const { tasks } = await buildExamples({ endpointID });
  console.log(JSON.stringify({ tasks: [tasks.sales, tasks.merge], input: await jsonFile('sales-report/rows.json') }));
} else {
  const store = new PostgresStore();
  const record = JSON.parse(readFileSync(0, 'utf8'));
  if (mode === 'transcript') {
    const rows = await store.thread(record.metadata.thread_id);
    // The pending call contains a private callback URL. Emit only the fields
    // needed to verify replay and the final answer.
    console.log(JSON.stringify(rows.map(row => ({
      role: row.role, final: row.content.final, text: row.role === 'assistant' ? row.content.text : undefined,
      call_id: row.content.tool_call?.id, dispatched: row.content.tool_call?.dispatched,
      delivery: row.content.report?.delivery,
    }))));
  } else {
    const platform = new GregalePlatform({ async fetchImpl(url, options) {
      const res = await fetch(url, options);
      const path = new URL(url).pathname;
      if (!res.ok && !path.startsWith('/identity')) {
        const error = await res.clone().json().catch(() => ({}));
        console.error(JSON.stringify({ http: res.status, path, code: error.code }));
      }
      if (res.ok && options?.method === 'POST' &&
          ((mode === 'drop-dispatch' && path === '/v1/agent-tools/invoke') ||
           (mode === 'drop-delivery' && path.endsWith('/run-artifact-deliveries/reports')))) {
        if (mode === 'drop-delivery') {
          const response = await res.json();
          if (response.status !== 'completed' || !response.receipt_url) throw new Error('Delivery did not commit');
          writeSync(1, response.receipt_url + '\n');
        } else {
          await res.arrayBuffer(); // RAP acknowledges dispatch with an empty 200.
        }
        process.exit(71); // Terminate after the server committed, before the Agent received its response.
      }
      return res;
    } });
    const agent = createReportAgent({ store, platform, destination: { app: 'report-agent', route: 'reports' } });
    const { final, ignored } = await agent(record);
    console.log(JSON.stringify({ final, ignored }));
  }
}
