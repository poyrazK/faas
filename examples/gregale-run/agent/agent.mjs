import { parseRecord, runSlice } from '../../../cmd/gregale/templates/agent-stateless-node/agent.js';
import { GregalePipelineTools } from '../../../cmd/gregale/templates/agent-stateless-node/tools.js';
import { reportModel, materializeCompletion } from './bridge.mjs';

// The HTTP trigger must acknowledge only after this promise resolves. Propagate
// failures so the inbox retries; keep the starter's authenticated read routes.
export function createReportAgent({ prefix = 'gr-demo', store, platform, reports, destination, loadUploadGrant }) {
  const pipelineTools = new GregalePipelineTools({ platform });
  const deps = { store, platform, model: reportModel({ prefix }), tools: { async dispatch(threadId, call) {
    const { upload_ref: uploadRef, ...input } = call.input;
    if (!uploadRef) return pipelineTools.dispatch(threadId, call);
    const grant = await loadUploadGrant?.({ threadId, callId: call.id, uploadRef });
    if (typeof grant !== 'string' || !grant.startsWith('agf1.')) throw new Error('No private grant for this report call');
    const [step] = input.steps;
    if (input.steps.length !== 1 || step.label !== 'csv-report') throw new Error('Unexpected report upload plan');
    return pipelineTools.dispatch(threadId, { ...call, input: { ...input, steps: [{ ...step, file_grants: [grant] }] } });
  } } };
  return async function processRecord(record) {
    const message = parseRecord(record);
    if (!message) return { ignored: true };
    // A committed final turn may already have revoked the source grant. The
    // starter finishes persisted dispatch/revocation work without rereading it.
    if (destination && message.role === 'tool' && await store.transaction(message.threadId, tx => tx.seen(message.messageId))) return runSlice(message, deps);
    return runSlice(await materializeCompletion(message, { platform, reports, destination }), deps);
  };
}
