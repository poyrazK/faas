export interface OperationWorkflowPolicyRequirement { milestone: string; rule_id: string; rule_version: string; code: string }
export interface OperationWorkflowPlannedDecision { milestone: string; decision: import('./customer-operation-decisions.js').OperationBusinessDecision }
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
export interface OperationWorkflowBlockerResolution { code: string; operation: string; description: string; blocker_operation_id: string; blocker_report_id: string; blocker_revision: number }
export interface OperationWorkflowBlocker { first_observed_at?: string; code: string; description: string; operation: string }
export interface OperationWorkflowEvidenceMilestone { id: string; name: string }
export interface OperationWorkflowDependency { subject_type: string; subject_id: string; workflow: string; instance_id: string; required_outcome_code?: string }
export interface OperationWorkflowRelatedInstance { dependency: OperationWorkflowDependency; status: 'unknown' | 'waiting' | 'terminal' | 'satisfied' | 'outcome_mismatch'; state?: OperationWorkflowState }
export interface OperationWorkflowState { depends_on?: OperationWorkflowDependency[]; dependencies_only?: boolean; outcome_code?: string; outcome_description?: string; outcome_only?: boolean; deadline_at?: string; deadline_only?: boolean; overdue: boolean; overdue_seconds?: number; report_id?: string; operation_id?: string; blocker_resolutions?: OperationWorkflowBlockerResolution[]; blockers?: OperationWorkflowBlocker[]; blockers_only?: boolean; workflow: string; instance_id: string; state: string; terminal: boolean; stale: boolean; occurred_at: string; stale_after_seconds?: number; revision: number; contract_version: number; evidence_milestones?: OperationWorkflowEvidenceMilestone[]; updated_at: string; platform_tenant_id?: string }
export interface OperationWorkflowStateHistoryEntry { depends_on?: OperationWorkflowDependency[]; dependencies_only?: boolean; outcome_code?: string; outcome_description?: string; outcome_only?: boolean; deadline_at?: string; deadline_only?: boolean; blocker_resolutions?: OperationWorkflowBlockerResolution[]; blockers?: OperationWorkflowBlocker[]; blockers_only?: boolean; id: string; operation_id: string; workflow: string; instance_id: string; from_state?: string; state: string; revision: number; contract_version: number; evidence_milestones?: OperationWorkflowEvidenceMilestone[]; occurred_at: string; published_at: string; platform_tenant_id?: string }
export interface OperationWorkflowInstanceMilestoneRef { id: string; operation_id: string; occurred_at: string; published_at: string }
export interface OperationWorkflowInstanceStep { step: string; label: string; operation?: string; operation_id?: string; milestone: string; position: number; observed: boolean; milestones_in_page: number; latest_milestone?: OperationWorkflowInstanceMilestoneRef; observed_in_retention: boolean; milestones_in_retention: number; latest_retained_milestone?: OperationWorkflowInstanceMilestoneRef }
export interface OperationWorkflowInstanceTransition { from: string; to: string; operation: string; required_dependency_workflows?: string[]; required_effects?: OperationWorkflowEffectRequirement[]; required_invariants?: OperationWorkflowInvariantRequirement[]; required_policies?: OperationWorkflowPolicyRequirement[]; required_milestones?: string[] }
export interface OperationWorkflowDecision { blockers?: OperationWorkflowBlocker[]; reason: 'state_unknown' | 'terminal' | 'no_declared_transition' | 'transitions_available' | 'state_stale' | 'application_blocked' | 'deadline_overdue' | 'dependency_waiting'; explanation: string; needs_attention: boolean; state_revision?: number; next_actions: OperationWorkflowInstanceTransition[] }
export interface OperationWorkflowInstanceSnapshot { readiness?: OperationWorkflowReadinessOverview; dependency_trace?: OperationWorkflowDependencyTrace; dependency_impact?: OperationWorkflowDependencyImpact; related_workflows?: OperationWorkflowRelatedInstance[]; decision?: OperationWorkflowDecision; workflow: string; instance_id: string; contract_version: number; state?: OperationWorkflowState; steps: OperationWorkflowInstanceStep[]; allowed_transitions?: OperationWorkflowInstanceTransition[]; transitions: OperationWorkflowStateHistoryEntry[]; has_more: boolean; next_milestone_cursor?: string; next_transition_cursor?: string }
export interface OperationWorkflowOutcomeEntry { app_id: string; scope: string; platform_tenant_id?: string; subject: OperationSubject; operation_id: string; state: OperationWorkflowState }
export interface OperationWorkflowOutcomesResponse { items: OperationWorkflowOutcomeEntry[]; evaluated_at: string; next_cursor?: string }
export interface OperationWorkflowOutcomeGroup { value: string; workflow_count: number }
export interface OperationWorkflowOutcomeSummary { group_by: 'outcome' | 'workflow' | 'customer'; evaluated_at: string; workflow_count: number; groups: OperationWorkflowOutcomeGroup[]; next_cursor?: string }
export interface OperationWorkflowOutcomeOptions { appID: string; scope: string; workflow?: string; code?: string; limit?: number; cursor?: string }
export interface OperationWorkflowOutcomeSummaryOptions extends OperationWorkflowOutcomeOptions { groupBy?: 'outcome' | 'workflow' }
export interface OperationWorkflowAttentionEntry { dependency_attention?: OperationWorkflowRelatedInstance[]; app_id: string; scope: string; platform_tenant_id?: string; subject: OperationSubject; operation_id: string; state: OperationWorkflowState; reasons: Array<'blocked' | 'stale' | 'overdue' | 'dependency'> }
export interface OperationWorkflowAttentionResponse { items: OperationWorkflowAttentionEntry[]; evaluated_at: string; next_cursor?: string }
export interface OperationWorkflowAttentionStats { dependency_workflow_count: number; dependency_count: number; overdue_workflow_count: number; earliest_overdue_deadline_at?: string; longest_overdue_seconds?: number; workflow_count: number; blocked_workflow_count: number; stale_workflow_count: number; blocker_count: number; unknown_age_blockers: number; oldest_blocker_at?: string; oldest_blocker_age_seconds?: number }
export interface OperationWorkflowAttentionGroup { value: string; stats: OperationWorkflowAttentionStats }
export interface OperationWorkflowAttentionSummary { group_by: 'workflow' | 'blocker_code' | 'target_operation' | 'dependency_status' | 'required_outcome_code' | 'customer'; evaluated_at: string; totals: OperationWorkflowAttentionStats; groups: OperationWorkflowAttentionGroup[]; next_cursor?: string }
export interface OperationWorkflowAttentionSummaryOptions extends OperationWorkflowAttentionOptions { groupBy?: 'workflow' | 'blocker_code' | 'target_operation' | 'dependency_status' | 'required_outcome_code' }
export interface OperationWorkflowAttentionOptions { dependencyStatus?: 'waiting' | 'unknown' | 'outcome_mismatch'; requiredOutcomeCode?: string; blockerCode?: string; appID: string; scope: string; workflow?: string; targetOperation?: string; reason?: 'blocked' | 'stale' | 'overdue' | 'dependency'; limit?: number; cursor?: string }
export interface OperationMilestonePageOptions { limit?: number; cursor?: string }
export interface OperationBusinessMilestoneOptions extends OperationMilestonePageOptions { appID: string; scope: string; subjectType: string; subjectID: string; workflow?: string; workflowInstanceID?: string; workflowStateCursor?: string; staleOnly?: boolean }
export interface OperationMilestones { milestones: OperationMilestone[]; workflow_states?: OperationWorkflowState[]; workflow_state_history?: OperationWorkflowStateHistoryEntry[]; workflow_instance?: OperationWorkflowInstanceSnapshot; next_cursor?: string; next_workflow_state_cursor?: string }
export interface Operation<T = unknown> {
  subject?: OperationSubject;
  id: string; name: string; generation: number; state: OperationState;
  progress?: OperationProgress; result?: T; artifacts?: OperationArtifact[]; completion_delivery: OperationDelivery;
  cancellation_requested: boolean; failure_code?: string; latest_sequence: number;
  created_at: string; updated_at: string; expires_at: string;
}
export interface OperationTenantIdentity { account_id: string; platform_tenant_id: string }
export interface OperationSubmissionScope { app_id: string; scope: string; name: string }
export interface OperationSubmissionFence { identity: OperationTenantIdentity; scope: OperationSubmissionScope }
export interface OperationSubmissionLookupOptions extends OperationSubmissionScope { idempotency_key: string; expected_identity?: OperationTenantIdentity }
export interface OperationSubmissionLookup { state: 'accepted' | 'unresolved' | 'expired'; receipt?: OperationReceipt; accepted_at?: string; idempotency_expires_at?: string }
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

function validSubmissionKey(key: string): boolean {
  return typeof key === 'string' && key.length > 0 && new TextEncoder().encode(key).length <= 128 && !/[\r\n\0]/.test(key);
}
/** @internal Shared submission preflight; invalid keys must not become a
 * feature controller's retained uncertain-submission identity. */
export function operationSubmissionIdentity(definition: string, key: string): void {
  if (!UUID.test(definition) || !validSubmissionKey(key) || !/^[\x20-\x7e]+$/.test(key)) throw new Error('Definition and bounded ASCII idempotency key required');
  // The saved key must be exactly what Fetch sends, before publishing a
  // durable receipt. Headers otherwise trim whitespace or reject Unicode.
  try {
    if (new Headers({ 'Idempotency-Key': key }).get('Idempotency-Key') !== key) throw new Error();
  } catch { throw new Error('An exact supported HTTP idempotency key is required'); }
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
  get apiURL(): string { return this.base.origin; }
  identity(signal?: AbortSignal): Promise<OperationTenantIdentity> { return this.request('/v1/platform-tenant-self/customer-operations/identity', 'GET', undefined, undefined, signal); }
  lookupSubmission(options: OperationSubmissionLookupOptions, signal?: AbortSignal): Promise<OperationSubmissionLookup> {
    if (!UUID.test(options.app_id) || !validSubmissionKey(options.idempotency_key)) throw new Error('Explicit app and bounded idempotency key required');
    if (!/^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$/.test(options.scope) || !/^[a-z][a-z0-9-]{0,63}$/.test(options.name)) throw new Error('Explicit submission app, environment and name required');
    return this.request('/v1/platform-tenant-self/customer-operations/submissions/lookup', 'POST', options, undefined, signal);
  }
  start(definition: string, input: unknown, idempotencyKey: string, signal?: AbortSignal, fence?: OperationSubmissionFence): Promise<OperationReceipt> {
    operationSubmissionIdentity(definition, idempotencyKey);
    return this.request('/v1/platform-tenant-self/customer-operations', 'POST', { definition_id: definition, input, ...(fence ? { expected_identity: fence.identity, expected_scope: fence.scope } : {}) }, { 'Idempotency-Key': idempotencyKey }, signal);
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
  workflowAttention(options: OperationWorkflowAttentionOptions, signal?: AbortSignal): Promise<OperationWorkflowAttentionResponse> {
    if (!UUID.test(options.appID) || !/^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$/.test(options.scope)) throw new Error('Explicit app and environment required');
    if (options.workflow !== undefined && !/^[a-z][a-z0-9-]{0,62}$/.test(options.workflow)) throw new Error('Invalid workflow name');
    if (options.targetOperation !== undefined && !/^[a-z][a-z0-9-]{0,63}$/.test(options.targetOperation)) throw new Error('Invalid target Operation');
    if (options.blockerCode !== undefined && !/^[a-z][a-z0-9-]{0,63}$/.test(options.blockerCode)) throw new Error('Invalid blocker code');
    if (options.dependencyStatus !== undefined && !['waiting','unknown','outcome_mismatch'].includes(options.dependencyStatus)) throw new Error('Invalid dependency status');
    if (options.requiredOutcomeCode !== undefined && !/^[a-z][a-z0-9-]{0,63}$/.test(options.requiredOutcomeCode)) throw new Error('Invalid required outcome');
    if (options.reason !== undefined && !['blocked', 'stale', 'overdue', 'dependency'].includes(options.reason)) throw new Error('Invalid attention reason');
    const query = milestonePageQuery(options);
    query.set('app_id', options.appID); query.set('scope', options.scope);
    if (options.workflow !== undefined) query.set('workflow', options.workflow);
    if (options.targetOperation !== undefined) query.set('target_operation', options.targetOperation);
    if (options.blockerCode !== undefined && !/^[a-z][a-z0-9-]{0,63}$/.test(options.blockerCode)) throw new Error('Invalid blocker code');
    if (options.blockerCode !== undefined) query.set('blocker_code', options.blockerCode);
    if (options.dependencyStatus !== undefined) query.set('dependency_status', options.dependencyStatus);
    if (options.requiredOutcomeCode !== undefined) query.set('required_outcome_code', options.requiredOutcomeCode);
    if (options.reason !== undefined) query.set('reason', options.reason);
    return this.request('/v1/platform-tenant-self/workflow-attention?' + query, 'GET', undefined, undefined, signal);
  }
  workflowAttentionSummary(options: OperationWorkflowAttentionSummaryOptions, signal?: AbortSignal): Promise<OperationWorkflowAttentionSummary> {
    if (!UUID.test(options.appID) || !/^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$/.test(options.scope)) throw new Error('Explicit app and environment required');
    if (options.workflow !== undefined && !/^[a-z][a-z0-9-]{0,62}$/.test(options.workflow)) throw new Error('Invalid workflow name');
    if (options.targetOperation !== undefined && !/^[a-z][a-z0-9-]{0,63}$/.test(options.targetOperation)) throw new Error('Invalid target Operation');
    if (options.blockerCode !== undefined && !/^[a-z][a-z0-9-]{0,63}$/.test(options.blockerCode)) throw new Error('Invalid blocker code');
    if (options.dependencyStatus !== undefined && !['waiting','unknown','outcome_mismatch'].includes(options.dependencyStatus)) throw new Error('Invalid dependency status');
    if (options.requiredOutcomeCode !== undefined && !/^[a-z][a-z0-9-]{0,63}$/.test(options.requiredOutcomeCode)) throw new Error('Invalid required outcome');
    if (options.reason !== undefined && !['blocked', 'stale', 'overdue', 'dependency'].includes(options.reason)) throw new Error('Invalid attention reason');
    if (options.groupBy !== undefined && !['workflow','blocker_code','target_operation','dependency_status','required_outcome_code'].includes(options.groupBy)) throw new Error('Invalid summary grouping');
    const query = milestonePageQuery(options);
    query.set('app_id', options.appID); query.set('scope', options.scope);
    if (options.workflow !== undefined) query.set('workflow', options.workflow);
    if (options.targetOperation !== undefined) query.set('target_operation', options.targetOperation);
    if (options.blockerCode !== undefined && !/^[a-z][a-z0-9-]{0,63}$/.test(options.blockerCode)) throw new Error('Invalid blocker code');
    if (options.blockerCode !== undefined) query.set('blocker_code', options.blockerCode);
    if (options.dependencyStatus !== undefined) query.set('dependency_status', options.dependencyStatus);
    if (options.requiredOutcomeCode !== undefined) query.set('required_outcome_code', options.requiredOutcomeCode);
    if (options.reason !== undefined) query.set('reason', options.reason);
    if (options.groupBy !== undefined) query.set('group_by', options.groupBy);
    return this.request('/v1/platform-tenant-self/workflow-attention/summary?' + query, 'GET', undefined, undefined, signal);
  }
  workflowOutcomes(options: OperationWorkflowOutcomeOptions, signal?: AbortSignal): Promise<OperationWorkflowOutcomesResponse> {
    if (!UUID.test(options.appID) || !/^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$/.test(options.scope)) throw new Error('Explicit app and environment required');
    if (options.workflow !== undefined && !/^[a-z][a-z0-9-]{0,62}$/.test(options.workflow)) throw new Error('Invalid workflow name');
    if (options.code !== undefined && !/^[a-z][a-z0-9-]{0,63}$/.test(options.code)) throw new Error('Invalid outcome code');
    const query = milestonePageQuery(options);
    query.set('app_id', options.appID); query.set('scope', options.scope);
    if (options.workflow !== undefined) query.set('workflow', options.workflow);
    if (options.code !== undefined) query.set('code', options.code);
    return this.request('/v1/platform-tenant-self/workflow-outcomes?' + query, 'GET', undefined, undefined, signal);
  }
  workflowOutcomeSummary(options: OperationWorkflowOutcomeSummaryOptions, signal?: AbortSignal): Promise<OperationWorkflowOutcomeSummary> {
    if (!UUID.test(options.appID) || !/^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$/.test(options.scope)) throw new Error('Explicit app and environment required');
    if (options.workflow !== undefined && !/^[a-z][a-z0-9-]{0,62}$/.test(options.workflow)) throw new Error('Invalid workflow name');
    if (options.code !== undefined && !/^[a-z][a-z0-9-]{0,63}$/.test(options.code)) throw new Error('Invalid outcome code');
    const query = milestonePageQuery(options);
    query.set('app_id', options.appID); query.set('scope', options.scope);
    if (options.workflow !== undefined) query.set('workflow', options.workflow);
    if (options.code !== undefined) query.set('code', options.code);
    if (options.groupBy !== undefined) { if (!['outcome','workflow'].includes(options.groupBy)) throw new Error('Invalid outcome grouping'); query.set('group_by',options.groupBy); }
    return this.request('/v1/platform-tenant-self/workflow-outcomes/summary?' + query, 'GET', undefined, undefined, signal);
  }
  workflowActionPreview(body: OperationWorkflowActionPreviewRequest, signal?: AbortSignal): Promise<OperationWorkflowActionPreviewResponse> {
    if (!UUID.test(body.app_id ?? '') || !/^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$/.test(body.scope)) throw new Error('Explicit app and environment required');
    if (body.tenant_id !== undefined) throw new Error('Customer ownership comes from authentication');
    return this.request('/v1/platform-tenant-self/workflow-actions/preview', 'POST', body, undefined, signal);
  }
  workflowReadiness(body: OperationWorkflowReadinessRequest, signal?: AbortSignal): Promise<OperationWorkflowReadinessResponse> {
    if (!UUID.test(body.app_id ?? '') || !/^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$/.test(body.scope)) throw new Error('Explicit app and environment required');
    if (body.tenant_id !== undefined) throw new Error('Customer ownership comes from authentication');
    return this.request('/v1/platform-tenant-self/workflow-readiness', 'POST', body, undefined, signal);
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
  cancel<T = unknown>(id: string, generation: number, signal?: AbortSignal): Promise<Operation<T>> {
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

export interface OperationWorkflowDependentInstance {
  subject: OperationSubject;
  state: OperationWorkflowState;
  required_outcome_code?: string;
  dependency_status: 'unknown' | 'waiting' | 'terminal' | 'satisfied' | 'outcome_mismatch';
  needs_attention: boolean;
}
export interface OperationWorkflowDependencyImpact {
  items: OperationWorkflowDependentInstance[];
  workflow_count: number;
  impacted_workflow_count: number;
  has_more: boolean;
}

export interface OperationWorkflowDependencyFinding {
  kind: 'reported_blockers' | 'state_unknown' | 'outcome_unknown' | 'outcome_mismatch' | 'awaiting_application' | 'state_stale' | 'deadline_overdue' | 'cycle' | 'trace_limit';
  path: OperationWorkflowDependency[];
  explanation: string;
  state?: OperationWorkflowState;
  limit?: 'depth' | 'workflows' | 'findings' | 'dependencies';
}
export interface OperationWorkflowDependencyTrace {
  findings: OperationWorkflowDependencyFinding[];
  visited_workflow_count: number;
  examined_dependency_count: number;
  depth_limit: number;
  workflow_limit: number;
  finding_limit: number;
  dependency_limit: number;
  truncated: boolean;
  limits_reached?: Array<'depth' | 'workflows' | 'findings' | 'dependencies'>;
}

export interface OperationWorkflowReadinessRequest {
 effects?: OperationWorkflowPlannedEffect[]; invariants?: OperationWorkflowPlannedInvariant[]; decisions?: OperationWorkflowPlannedDecision[];
 app_id?: string; tenant_id?: string; scope: string; subject: OperationSubject;
 workflow: string; instance_id: string; operation: string; from_state: string; to_state: string;
 milestones?: string[]; state_revision?: number; contract_version?: number;
}
export interface OperationWorkflowTransitionReadiness {
 unmet_effects?: OperationWorkflowUnmetEffect[]; unmet_invariants?: OperationWorkflowUnmetInvariant[]; invariant_blockers?: OperationWorkflowBlocker[]; missing_dependency_workflows?: string[]; missing_policies?: OperationWorkflowPolicyRequirement[];
 transition: OperationWorkflowInstanceTransition; declared: boolean; ready: boolean;
 reasons: Array<'state_unknown'|'terminal'|'transition_undeclared'|'from_state_mismatch'|'revision_mismatch'|'contract_version_mismatch'|'application_blocked'|'dependency_unmet'|'milestone_required'|'policy_evidence_required'|'dependency_required'|'invariant_evidence_required'|'effect_evidence_required'>;
 advisories: Array<'state_stale'|'deadline_overdue'>;
 state_revision?: number; contract_version: number; blockers: OperationWorkflowBlocker[];
 unmet_dependencies: OperationWorkflowRelatedInstance[]; missing_milestones: string[];
}
export interface OperationWorkflowReadinessResponse {
 subject: OperationSubject; workflow: string; instance_id: string; evaluated_at: string;
 readiness: OperationWorkflowTransitionReadiness;
}
export interface OperationWorkflowReadinessOverview {
 items: OperationWorkflowTransitionReadiness[]; transition_count: number; has_more: boolean;
}

export interface OperationWorkflowActionPreviewRequest {
 app_id?: string; tenant_id?: string; scope: string; subject: OperationSubject;
 workflow: string; instance_id: string; operation?: string; state_revision?: number; contract_version?: number;
}
export interface OperationWorkflowActionPreviewResponse {
 subject: OperationSubject; workflow: string; instance_id: string; evaluated_at: string;
 state?: OperationWorkflowState; state_revision?: number; contract_version: number;
 reason: 'state_unknown'|'terminal'|'no_declared_transition'|'actions_available';
 actions: OperationWorkflowTransitionReadiness[]; action_count: number; has_more: boolean;
}

export interface OperationWorkflowInvariantRequirement {milestone: string; code: string; version: string}
export interface OperationWorkflowPlannedInvariant {milestone: string; invariant: import('./customer-operation-invariants.js').OperationBusinessInvariant}
export interface OperationWorkflowUnmetInvariant {requirement: OperationWorkflowInvariantRequirement; reason: 'missing'|'mismatched'|'failed'|'unknown'}

export interface OperationWorkflowEffectRequirement {milestone: string; code: string; version: string}
export interface OperationWorkflowPlannedEffect {milestone: string; effect: import('./customer-operation-effects.js').OperationBusinessEffect}
export interface OperationWorkflowUnmetEffect {requirement: OperationWorkflowEffectRequirement; reason: 'missing'|'mismatched'|'pending'|'failed'}
