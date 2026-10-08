import { OPERATION_SUBJECT_ID_BYTES } from './operation-contract.js';
import { parseFrame } from './sse.js';
import type { OperationWorkflowStep } from './generated/models/OperationWorkflowStep.js';

export type OperationState = 'accepted' | 'running' | 'succeeded' | 'failed' | 'cancelled' | 'requires_reconciliation';
export interface OperationProgress { stage: string; completed: number; total: number; attempt: number; updated_at: string }
export interface OperationDelivery { state: string; delivery_id?: string; attempts: number; last_error?: string; next_attempt_at?: string }
export interface OperationArtifact { id: string; name: string; uri: string; size_bytes: number; sha256: string; expires_at?: string }
export interface OperationArtifactReport { report_id: string; name: string; uri: string; size_bytes: number; sha256: string }
export interface OperationSubject { type: string; id: string }
export interface OperationMilestoneReport { id: string; name: string; payload: unknown; occurred_at: string }
export interface OperationMilestone extends OperationMilestoneReport { operation_id: string; subject?: OperationSubject; platform_tenant_id?: string; workflow_steps?: OperationWorkflowStep[]; created_at: string; sequence: number }
export interface OperationWorkflowState { workflow: string; instance_id: string; state: string; terminal: boolean; stale: boolean; occurred_at: string; stale_after_seconds?: number; revision: number; updated_at: string; platform_tenant_id?: string }
export interface OperationWorkflowStateHistoryEntry { id: string; operation_id: string; workflow: string; instance_id: string; from_state?: string; state: string; revision: number; occurred_at: string; published_at: string; platform_tenant_id?: string }
export interface OperationMilestonePageOptions { limit?: number; cursor?: string }
export interface OperationBusinessMilestoneOptions extends OperationMilestonePageOptions { appID: string; scope: string; subjectType: string; subjectID: string; workflow?: string; workflowInstanceID?: string; workflowStateCursor?: string; staleOnly?: boolean }
export interface OperationMilestones { milestones: OperationMilestone[]; workflow_states?: OperationWorkflowState[]; workflow_state_history?: OperationWorkflowStateHistoryEntry[]; next_cursor?: string; next_workflow_state_cursor?: string }
export interface Operation<T = unknown> {
  subject?: OperationSubject;
  id: string; name: string; generation: number; state: OperationState;
  progress?: OperationProgress; result?: T; artifacts?: OperationArtifact[]; completion_delivery: OperationDelivery;
  cancellation_requested: boolean; failure_code?: string; latest_sequence: number;
  created_at: string; updated_at: string; expires_at: string;
}
export interface OperationReceipt { id: string; status_url: string; events_url: string }
export type OperationSummary = Omit<Operation, 'result' | 'artifacts' | 'failure_code' | 'completion_delivery'> & { completion_delivery: Pick<OperationDelivery, 'state' | 'attempts' | 'next_attempt_at'> };
export interface OperationList { operations: OperationSummary[]; next_cursor?: string }
export interface OperationListOptions { subjectType?: string; subjectID?: string; appID: string; scope: string; name?: string; state?: OperationState; limit?: number; cursor?: string }
export interface OperationEvent { operation_id: string; sequence: number; type: string; execution_id?: string; attempt?: number; data: unknown; created_at: string }
export interface OperationEvents { events: OperationEvent[]; latest_sequence: number; resync_required: boolean }
export interface OperationReport { report_id: string; stage: string; completed: number; total: number }
export interface OperationClientOptions {
  apiURL: string;
  /** Obtain a current tenant-bound token from your application's authenticated backend.
   * Called again on every reconnect. Account API keys must stay on your server. */
  credential: () => string | Promise<string>;
  fetch?: typeof globalThis.fetch;
}
export class OperationHTTPError extends Error {
  constructor(public readonly status: number, public readonly code: string) { super(`Operation request failed (${status}: ${code})`); this.name = 'OperationHTTPError'; }
}

class OperationProtocolError extends Error {}

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const MAX_BODY = 2 * 1024 * 1024;
export function operationAPIBase(value: string): URL {
  const url = new URL(value);
  if (url.username || url.password || url.search || url.hash || (url.protocol !== 'https:' && !(url.protocol === 'http:' && ['localhost', '127.0.0.1', '[::1]'].includes(url.hostname)))) throw new Error('Operations API requires HTTPS or loopback HTTP');
  return url;
}
export async function operationJSON(response: Response): Promise<unknown> {
  if (!response.body) throw new Error('Missing operation response');
  const reader = response.body.getReader(); const chunks: Uint8Array[] = []; let size = 0;
  try {
    for (;;) { const { value, done } = await reader.read(); if (done) break; size += value.length; if (size > MAX_BODY) throw new Error('Operation response exceeds its bound'); chunks.push(value); }
    const bytes = new Uint8Array(size); let offset = 0;
    for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.length; }
    return JSON.parse(new TextDecoder().decode(bytes)) as unknown;
  } finally { await reader.cancel().catch(() => {}); reader.releaseLock(); }
}
export async function operationResponse<T>(response: Response): Promise<T> {
  const data = await operationJSON(response) as { code?: string };
  if (!response.ok) throw new OperationHTTPError(response.status, typeof data?.code === 'string' ? data.code : 'operation_request_failed');
  return data as T;
}
function operationPath(id: string): string {
  if (!UUID.test(id)) throw new Error('Invalid operation identity');
  return `/v1/platform-tenant-self/customer-operations/${id}`;
}

function milestonePageQuery(options: OperationMilestonePageOptions): URLSearchParams {
  if (options.limit !== undefined && (!Number.isSafeInteger(options.limit) || options.limit < 1 || options.limit > 100)) throw new Error('Invalid milestone page size');
  if (options.cursor !== undefined && (!options.cursor || options.cursor.length > 512)) throw new Error('Invalid milestone cursor');
  const query = new URLSearchParams();
  if (options.limit !== undefined) query.set('limit', String(options.limit));
  if (options.cursor !== undefined) query.set('cursor', options.cursor);
  return query;
}

/** Browser-safe customer status/progress client. Business and delivery state
 * remain separate, including after reconnect or notification failure. */
export class GregaleOperationClient {
  private readonly base: URL;
  private readonly fetchImpl: typeof globalThis.fetch;
  constructor(private readonly options: OperationClientOptions) { this.base = operationAPIBase(options.apiURL); this.fetchImpl = options.fetch ?? globalThis.fetch.bind(globalThis); }
  private async request<T>(path: string, method: string, body?: unknown, headers?: HeadersInit, signal?: AbortSignal): Promise<T> {
    const auth = await this.options.credential();
    if (!auth || /[\r\n]/.test(auth)) throw new Error('Operation credential unavailable');
    const h = new Headers(headers); h.set('Authorization', `Bearer ${auth}`); h.set('Content-Type', 'application/json');
    const response = await this.fetchImpl(new URL(path, this.base), { method, headers: h, ...(body === undefined ? {} : { body: JSON.stringify(body) }), signal, redirect: 'error', cache: 'no-store' });
    return operationResponse<T>(response);
  }
  start(definition: string, input: unknown, idempotencyKey: string, signal?: AbortSignal): Promise<OperationReceipt> {
    if (!UUID.test(definition) || !idempotencyKey || new TextEncoder().encode(idempotencyKey).length > 128 || /[\r\n\0]/.test(idempotencyKey)) throw new Error('Definition and bounded idempotency key required');
    return this.request('/v1/platform-tenant-self/customer-operations', 'POST', { definition_id: definition, input }, { 'Idempotency-Key': idempotencyKey }, signal);
  }
  get<T = unknown>(id: string, signal?: AbortSignal): Promise<Operation<T>> { return this.request(operationPath(id), 'GET', undefined, undefined, signal); }
  list(options: OperationListOptions, signal?: AbortSignal): Promise<OperationList> {
    if (!UUID.test(options.appID) || !/^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$/.test(options.scope)) throw new Error('Explicit app and environment required');
    if (options.limit !== undefined && (!Number.isSafeInteger(options.limit) || options.limit < 1 || options.limit > 100)) throw new Error('Invalid operation page size');
    if (options.name !== undefined && !/^[a-z][a-z0-9-]{0,63}$/.test(options.name)) throw new Error('Invalid operation name');
    if (options.state !== undefined && !['accepted', 'running', 'succeeded', 'failed', 'cancelled', 'requires_reconciliation'].includes(options.state)) throw new Error('Invalid operation state');
    if (options.cursor !== undefined && (!options.cursor || options.cursor.length > 512)) throw new Error('Invalid operation page cursor');
    if (options.subjectType !== undefined || options.subjectID !== undefined) {
      if (options.subjectType === undefined || !/^[a-z][a-z0-9-]{0,63}$/.test(options.subjectType) || options.subjectID === undefined || !options.subjectID || new TextEncoder().encode(options.subjectID).length > OPERATION_SUBJECT_ID_BYTES || /[\x00-\x1f\x7f]/.test(options.subjectID) || /[\uD800-\uDFFF]/u.test(options.subjectID)) throw new Error('Valid paired business reference required');
    }
    const query = new URLSearchParams({ app_id: options.appID, scope: options.scope });
    if (options.subjectType !== undefined) query.set('subject_type', options.subjectType);
    if (options.subjectID !== undefined) query.set('subject_id', options.subjectID);
    if (options.name !== undefined) query.set('name', options.name);
    if (options.state !== undefined) query.set('state', options.state);
    if (options.limit !== undefined) query.set('limit', String(options.limit));
    if (options.cursor !== undefined) query.set('cursor', options.cursor);
    return this.request('/v1/platform-tenant-self/customer-operations?' + query, 'GET', undefined, undefined, signal);
  }
  milestones(id: string, options: OperationMilestonePageOptions = {}, signal?: AbortSignal): Promise<OperationMilestones> {
    return this.request(operationPath(id) + '/milestones?' + milestonePageQuery(options), 'GET', undefined, undefined, signal);
  }
  businessMilestones(options: OperationBusinessMilestoneOptions, signal?: AbortSignal): Promise<OperationMilestones> {
    if (!UUID.test(options.appID) || !/^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$/.test(options.scope)) throw new Error('Explicit app and environment required');
    if (!/^[a-z][a-z0-9-]{0,63}$/.test(options.subjectType) || !options.subjectID || new TextEncoder().encode(options.subjectID).length > OPERATION_SUBJECT_ID_BYTES || /[\x00-\x1f\x7f]/.test(options.subjectID) || /[\uD800-\uDFFF]/u.test(options.subjectID)) throw new Error('Valid paired business reference required');
    if (options.workflow !== undefined || options.workflowInstanceID !== undefined) {
      if (options.workflow === undefined || !/^[a-z][a-z0-9-]{0,62}$/.test(options.workflow) || options.workflowInstanceID === undefined || !options.workflowInstanceID || new TextEncoder().encode(options.workflowInstanceID).length > 256 || /[\x00-\x1f\x7f]/.test(options.workflowInstanceID) || /[\uD800-\uDFFF]/u.test(options.workflowInstanceID)) throw new Error('Valid workflow and instance ID required together');
    }
    if (options.workflowStateCursor !== undefined && (!options.workflow || !options.workflowInstanceID || !options.workflowStateCursor || options.workflowStateCursor.length > 512)) throw new Error('Workflow state cursor requires a workflow and instance ID');
    const query = milestonePageQuery(options);
    query.set('app_id', options.appID); query.set('scope', options.scope);
    query.set('subject_type', options.subjectType); query.set('subject_id', options.subjectID);
    if (options.workflow !== undefined && options.workflowInstanceID !== undefined) {
      query.set('workflow', options.workflow); query.set('workflow_instance_id', options.workflowInstanceID);
    }
    if (options.workflowStateCursor !== undefined) query.set('workflow_state_cursor', options.workflowStateCursor);
    if (options.staleOnly) query.set('stale_only', 'true');
    return this.request('/v1/platform-tenant-self/customer-operation-milestones?' + query, 'GET', undefined, undefined, signal);
  }
  async download(id: string, artifactID: string, signal?: AbortSignal): Promise<Response> {
    if (!UUID.test(artifactID)) throw new Error('Invalid operation artifact identity');
    const token = await this.options.credential();
    if (!token || /[\r\n]/.test(token)) throw new Error('Operation credential unavailable');
    const response = await this.fetchImpl(new URL(operationPath(id) + `/artifacts/${artifactID}`, this.base), { headers: { Authorization: `Bearer ${token}` }, signal, cache: 'no-store', redirect: 'error' });
    if (!response.ok) await operationResponse(response);
    return response;
  }
  cancel(id: string, generation: number, signal?: AbortSignal): Promise<Operation> {
    if (!Number.isSafeInteger(generation) || generation < 1) throw new Error('Current operation generation required');
    return this.request(operationPath(id) + '/cancel', 'POST', { expected_generation: generation }, undefined, signal);
  }
  events(id: string, after = 0, signal?: AbortSignal): Promise<OperationEvents> {
    if (!Number.isSafeInteger(after) || after < 0) throw new Error('Invalid operation cursor');
    return this.request(operationPath(id) + `/events?after=${after}`, 'GET', undefined, undefined, signal);
  }
  async *subscribe<T = unknown>(id: string, options: { after?: number; signal: AbortSignal }): AsyncGenerator<{ event?: OperationEvent; snapshot?: Operation<T>; resync?: boolean }> {
    let after = options.after ?? 0;
    if (!Number.isSafeInteger(after) || after < 0) throw new Error('Invalid operation cursor');
    const path = operationPath(id) + '/events'; let failures = 0;
    while (!options.signal.aborted) {
      let response: Response;
      try {
        const token = await this.options.credential();
        if (!token || /[\r\n]/.test(token)) throw new OperationProtocolError('Operation credential unavailable');
        response = await this.fetchImpl(new URL(path, this.base), { headers: { Authorization: `Bearer ${token}`, Accept: 'text/event-stream', 'Last-Event-ID': String(after) }, signal: options.signal, cache: 'no-store', redirect: 'error' });
        if (response.status === 401) { await response.body?.cancel(); throw new Error('Operation credential needs refresh'); }
        if (!response.ok) { await operationResponse(response); throw new Error('Unreachable operation response'); }
        if (!response.headers.get('content-type')?.startsWith('text/event-stream') || !response.body) throw new OperationProtocolError('Operation stream unavailable');
        const reader = response.body.getReader(); const decoder = new TextDecoder(); let buffer = ''; failures = 0;
        try {
          for (;;) {
            const { value, done } = await reader.read(); if (done) break;
            buffer += decoder.decode(value, { stream: true });
            if (buffer.length > MAX_BODY) throw new OperationProtocolError('Operation frame exceeds its bound');
            let frame: ReturnType<typeof parseFrame>;
            while ((frame = parseFrame(buffer)) !== null) {
              buffer = buffer.slice(frame.consumed);
              if (!frame.event.data) continue;
              if (frame.event.event === 'auth_expired') throw new Error('Operation credential needs refresh');
              if (frame.event.event === 'unavailable') throw new OperationHTTPError(410, 'operation_unavailable');
              if (frame.event.event === 'snapshot' || frame.event.event === 'resync') {
                const snapshot = JSON.parse(frame.event.data) as Operation<T>;
                if (snapshot.id !== id || !Number.isSafeInteger(snapshot.latest_sequence) || snapshot.latest_sequence < 0) throw new OperationProtocolError('Invalid operation snapshot');
                if (frame.event.event === 'resync') after = snapshot.latest_sequence;
                yield { snapshot, resync: frame.event.event === 'resync' };
              } else if (frame.event.event === 'operation') {
                const event = JSON.parse(frame.event.data) as OperationEvent;
                if (event.operation_id !== id || !Number.isSafeInteger(event.sequence) || frame.event.id !== String(event.sequence) || event.sequence > after + 1) throw new OperationProtocolError('Invalid operation event cursor');
                if (event.sequence <= after) continue;
                // Advance only when the consumer resumes after applying this event.
                yield { event }; after = event.sequence;
              }
            }
          }
        } finally { await reader.cancel().catch(() => {}); reader.releaseLock(); }
      } catch (err) {
        if (options.signal.aborted) return;
        if (err instanceof OperationHTTPError || err instanceof OperationProtocolError || err instanceof SyntaxError) throw err;
        if (++failures >= 5) throw err;
      }
      await new Promise<void>(resolve => {
        const onAbort = (): void => { clearTimeout(timer); resolve(); };
        const timer = setTimeout(() => { options.signal.removeEventListener('abort', onAbort); resolve(); }, Math.min(5000, 250 * 2 ** failures));
        options.signal.addEventListener('abort', onAbort, { once: true });
      });
    }
  }
}
