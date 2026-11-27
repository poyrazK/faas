import { PostgresStore } from '../../../cmd/gregale/templates/agent-stateless-node/store.js';
import { GregalePlatform } from '../../../cmd/gregale/templates/agent-stateless-node/platform.js';
import { createReportAgent } from './agent.mjs';
import { createHostedReportHandler } from './trigger.mjs';

let handle;
export function handler(event, context) {
  if (!handle) {
    const endpointID = process.env.REPORT_ENDPOINT_ID;
    const app = process.env.REPORT_APP;
    const route = process.env.REPORT_UPLOAD_ROUTE;
    if (!app || !route) throw new Error('Configure REPORT_APP and REPORT_UPLOAD_ROUTE');
    const store = new PostgresStore();
    const platform = new GregalePlatform();
    const processRecord = createReportAgent({
      prefix: process.env.REPORT_TASK_PREFIX, store, platform, destination: { app, route },
      loadUploadGrant: ({ threadId, callId, uploadRef }) => store.withClient(async client => {
        const { rows } = await client.query(
          'SELECT grant_token FROM report_upload_grants WHERE endpoint_id=$1 AND thread_id=$2 AND call_id=$3 AND upload_ref=$4',
          [endpointID, threadId, callId, uploadRef]);
        return rows[0]?.grant_token;
      }),
    });
    handle = createHostedReportHandler({ endpointID, processRecord });
  }
  return handle(event, context);
}
