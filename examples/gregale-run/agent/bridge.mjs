import { createHash } from 'node:crypto';
import { names, pipelineArguments } from '../scripts/manifests.mjs';

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export function regionalReportArguments(taskNames, rows) {
  if (!Array.isArray(rows) || rows.length < 2 || rows.length > 20) throw new Error('Agent demo expects 2..20 orders');
  const orders = Array.from(rows, row => {
    if (!row || Object.keys(row).sort().join(',') !== 'order_id,quantity,region,unit_price_cents' ||
        typeof row.order_id !== 'string' || !row.order_id.trim() || row.order_id.length > 48 ||
        !['north', 'south'].includes(row.region) ||
        !Number.isSafeInteger(row.quantity) || row.quantity < 1 || row.quantity > 1000000 ||
        !Number.isSafeInteger(row.unit_price_cents) || row.unit_price_cents < 0 || row.unit_price_cents > 1000000000) {
      throw new Error('Invalid Agent order');
    }
    const { order_id, region, quantity, unit_price_cents } = row;
    return { order_id, region, quantity, unit_price_cents };
  });
  return pipelineArguments(taskNames, orders);
}

// A deterministic model step: no model account or external network required.
// An LLM can select the same tool after producing the same bounded JSON input.
export function reportModel({ prefix = 'gr-demo' } = {}) {
  const taskNames = names(prefix);
  return { next(_history, message) {
    if (message.role === 'user') {
      const ref = message.content?.upload_ref;
      if (typeof ref === 'string') {
        if (!uuid.test(ref) || Object.keys(message.content).length !== 1) throw new Error('Invalid report upload reference');
        return { call: { tool: 'gregale_pipeline', input: { upload_ref: ref,
          steps: [{ label: 'csv-report', task: { name: taskNames.sales, version: 1, input: { csv_file: 'orders.csv' } } }] } } };
      }
      const input = regionalReportArguments(taskNames, message.content?.rows);
      // Leave room for the callback and RAP envelope in its 8 KiB limit.
      if (Buffer.byteLength(JSON.stringify(input)) > 5000) throw new Error('Agent arguments too large');
      return { call: { tool: 'gregale_pipeline', input } };
    }
    const report = message.content?.report;
    if (!report) throw new Error('Materialize the pipeline completion before the next Agent slice');
    if (report.outcome !== 'succeeded') return { text: `Report did not complete: ${report.outcome}.`, final: true };
    return { text: `${report.summary.orders} orders, ${report.summary.revenue_cents} USD cents. Report: ${report.report_ref}`, final: true };
  } };
}

export async function inspectReportExport(completion, readRun) {
  const csv = completion.steps.length === 1 && completion.steps[0].label === 'csv-report';
  const output = completion.steps.find(step => step.label === (csv ? 'csv-report' : 'merge'));
  const outputName = csv ? 'report.html' : 'combined.html';
  const runID = output?.reused_run_id ?? output?.attempts?.at(-1)?.run_id;
  if (output?.outcome !== 'succeeded' || !runID) throw new Error('Missing successful report');
  const run = await readRun(runID);
  const summary = run.result;
  if (run.id !== runID || run.status !== 'succeeded' || run.result_validation?.status === 'invalid' ||
      summary?.currency !== 'USD' || (!csv && summary.source_reports !== 2) ||
      ['orders', 'units', 'revenue_cents'].some(field => !Number.isSafeInteger(summary[field]) || summary[field] < 0)) throw new Error('Invalid report result');
  const artifact = run.artifacts?.find(file => file.name === outputName);
  const digest = artifact?.sha256?.replace(/^sha256:/, '');
  if (!artifact || !Number.isSafeInteger(artifact.size_bytes) || artifact.size_bytes < 1 || artifact.size_bytes > 32768 ||
      !/^[a-f0-9]{64}$/.test(digest ?? '')) throw new Error('Invalid report artifact metadata');
  return { summary, runID, outputName, artifact, digest };
}

export function reportDeliveryRequest(completionID, { runID, outputName, artifact, digest }, suffix = '') {
  return {
    idempotency_key: `${completionID}:${outputName}${suffix}`,
    execution_id: runID, artifact_name: outputName, size_bytes: artifact.size_bytes,
    sha256: `sha256:${digest}`, content_type: 'text/html; charset=utf-8',
  };
}

// Call AFTER parseRecord, BEFORE runSlice. The starter revokes the thread grant
// after a final answer; scoped Gregale download links then stop working.
// reports.get/putOnce must use durable, tenant-scoped app storage. putOnce stores
// bytes and metadata atomically and returns the same record on repeated calls.
export async function materializeCompletion(message, { platform, reports, destination }) {
  if (message.role !== 'tool') return message;
  const envelope = message.content;
  if (envelope?.type !== 'tool_result' || typeof envelope.text !== 'string') throw new Error('Expected RAP tool result');
  const result = JSON.parse(envelope.text);
  if (!uuid.test(result.completion_id ?? '')) throw new Error('Invalid completion ID');
  const key = JSON.stringify([message.endpointId, message.threadId, result.completion_id]);
  // Redelivery can arrive after revocation; consult app storage before Gregale.
  if (!reports && !destination) throw new Error('Configure managed delivery or durable report storage');
  let report = await reports?.get(key);
  if (!report) {
    const receipt = await platform.pipelineReceipt(result.completion_id);
    const completion = receipt.completion;
    if (receipt.id !== result.completion_id || completion?.completion_id !== result.completion_id ||
        completion.workflow_id !== result.workflow_id || completion.outcome !== result.outcome) throw new Error('Completion does not match receipt');
    if (!['succeeded', 'failed', 'cancelled', 'deadline_exceeded'].includes(completion.outcome)) throw new Error('Pipeline is not terminal');
    if (completion.outcome !== 'succeeded') {
      report = reports ? await reports.putOnce(key, { outcome: completion.outcome }) : { outcome: completion.outcome };
    } else {
      const output = await inspectReportExport(completion, id => platform.taskReceipt(id));
      const { summary, runID, outputName, artifact, digest } = output;
      if (destination) {
        const delivery = await platform.deliverRunArtifact(destination.app, destination.route,
          reportDeliveryRequest(result.completion_id, output, destination.keySuffix));
        if (delivery.status !== 'completed' || !delivery.object) throw new Error(`Report delivery ${delivery.id} is ${delivery.status}; inspect the same receipt before finalizing`);
        report = { outcome: 'succeeded', summary, report_ref: delivery.receipt_url, delivery };
      } else {
        const response = await platform.taskArtifact(runID, outputName);
        if (!response.ok || !response.body) throw new Error('Report download failed');
        const chunks = []; let size = 0;
        for await (const chunk of response.body) {
          size += chunk.byteLength;
          if (size > artifact.size_bytes) throw new Error('Report size mismatch');
          chunks.push(Buffer.from(chunk));
        }
        const bytes = Buffer.concat(chunks);
        if (size !== artifact.size_bytes || createHash('sha256').update(bytes).digest('hex') !== digest) throw new Error('Report checksum mismatch');
        report = await reports.putOnce(key, { outcome: 'succeeded', summary, bytes, media_type: 'text/html; charset=utf-8' });
      }
    }
  }
  if (report.outcome === 'succeeded' && (typeof report.report_ref !== 'string' || !report.report_ref)) throw new Error('App storage must return a report_ref');
  // Keep binary data out of the inbox transcript and model context.
  const { outcome, summary, report_ref, delivery } = report;
  return { ...message, content: { ...envelope, report: { outcome, summary, report_ref, ...(delivery ? { delivery } : {}) } } };
}
