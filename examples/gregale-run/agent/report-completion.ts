import { verifyWebhook } from '../../../sdk/node/src/webhook.ts';
import { REPORT_UUID } from './report-regional.ts';
import type { PipelineFinished } from './report-regional.ts';

const EVENT = 'execution.pipeline.finished';
const MAX_BODY_BYTES = 16 * 1024;
const WORKFLOW_PREFIX = 'report-';

async function readBody(body: ReadableStream<Uint8Array> | null) {
  if (!body) return Buffer.alloc(0);
  const reader = body.getReader();
  const chunks: Uint8Array[] = [];
  let bytes = 0;
  try {
    for (;;) {
      const { value, done } = await reader.read();
      if (done) return Buffer.concat(chunks, bytes);
      bytes += value.byteLength;
      if (bytes > MAX_BODY_BYTES) {
        await reader.cancel();
        return null;
      }
      chunks.push(value);
    }
  } finally {
    reader.releaseLock();
  }
}

type Envelope = {
  id?: string;
  type?: string;
  specversion?: string;
  data?: unknown;
  rule_name?: string;
  payload?: { event?: string; delivery_id?: string; data?: unknown };
};

function getCompletion(body: Buffer, deliveryID: string): PipelineFinished | null {
  const envelope = JSON.parse(body.toString('utf8')) as Envelope | null;
  if (envelope?.id !== deliveryID) throw new Error('Invalid webhook envelope');
  const cloud = envelope.specversion === '1.0';
  const type = cloud ? envelope.type : envelope.rule_name;
  if (type !== EVENT) return null;
  if (!cloud && (envelope.payload?.event !== EVENT || envelope.payload.delivery_id !== deliveryID)) {
    throw new Error('Invalid webhook envelope');
  }
  const event = (cloud ? envelope.data : envelope.payload?.data) as Partial<PipelineFinished> | null | undefined;
  if (typeof event?.workflow_id !== 'string') throw new Error('Missing webhook workflow');
  if (!event.workflow_id.startsWith(WORKFLOW_PREFIX) || !REPORT_UUID.test(event.workflow_id.slice(WORKFLOW_PREFIX.length))) return null;
  const { event_id, workflow_id, outcome } = event;
  if (typeof event_id !== 'string' || !REPORT_UUID.test(event_id) ||
      (outcome !== 'succeeded' && outcome !== 'failed' && outcome !== 'cancelled' && outcome !== 'deadline_exceeded')) {
    throw new Error('Invalid pipeline completion');
  }
  return { event_id, workflow_id, outcome };
}

export function createReportCompletionReceiver(secret: string, complete: (event: PipelineFinished) => Promise<boolean>) {
  if (!secret.trim()) throw new Error('Configure a non-empty report webhook signing secret');
  return async function receiveCompletion(request: Request): Promise<Response> {
    if (request.method !== 'POST') return new Response(null, { status: 405, headers: { Allow: 'POST' } });
    let body;
    try { body = await readBody(request.body); }
    catch { return new Response(null, { status: 400 }); }
    if (body === null) return new Response(null, { status: 413 });
    let deliveryID;
    try { ({ deliveryId: deliveryID } = verifyWebhook(secret, request.headers, body)); }
    catch { return new Response(null, { status: 401 }); }
    let event;
    try { event = getCompletion(body, deliveryID); }
    catch { return new Response(null, { status: 400 }); }
    try {
      if (!event || await complete(event)) return new Response(null, { status: 204 });
    } catch {
      return new Response('Report completion could not be processed', { status: 503 });
    }
    return new Response('Private report delivery is pending', { status: 503 });
  };
}
