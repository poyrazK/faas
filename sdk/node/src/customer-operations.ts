import type { OperationBusinessConditionDefinition } from './customer-operation-decisions.js';
export interface OperationWorkflowPolicyRequirement { milestone: string; rule_id: string; rule_version: string; code: string }
export interface OperationWorkflowPlannedDecision { milestone: string; decision: import('./customer-operation-decisions.js').OperationBusinessDecision }
import { OPERATION_WORKFLOW_BLOCKER_ACTOR_BYTES, OPERATION_SUBJECT_ID_BYTES } from './operation-contract.js';
import { parseFrame } from './sse.js';
import type { OperationWorkflowStep } from './generated/models/OperationWorkflowStep.js';

export type OperationState = 'accepted' | 'running' | 'waiting' | 'succeeded' | 'failed' | 'cancelled' | 'requires_reconciliation';
/** ADR-1194: why a waiting operation holds no execution, and until when. */
export interface OperationWaitingState { kind: 'timer' | 'approval' | 'event'; step: string; since: string; until?: string; approver?: 'account' | 'customer'; event?: string }
/** ADR-1196: bounds fixed onto an accepted Operation, and what it has used. */
export interface OperationLimits { deadline_seconds?: number; max_attempts?: number; max_execution_seconds?: number }
export interface OperationUsage { execution_ms: number; attempts: number }
export interface OperationProgress { stage: string; completed: number; total: number; attempt: number; updated_at: string }
export interface OperationDelivery { state: string; delivery_id?: string; attempts: number; last_error?: string; next_attempt_at?: string }
export interface OperationArtifact { id: string; name: string; uri: string; size_bytes: number; sha256: string; expires_at?: string }
export interface OperationArtifactReport { report_id: string; name: string; uri: string; size_bytes: number; sha256: string }
export interface OperationSubject { type: string; id: string }
export interface OperationMilestoneReport { id: string; name: string; payload: unknown; occurred_at: string }
export interface OperationMilestone extends OperationMilestoneReport { operation_id: string; subject?: OperationSubject; platform_tenant_id?: string; workflow_steps?: OperationWorkflowStep[]; created_at: string; sequence: number }
export interface OperationWorkflowBlockerResolution { verification_milestone_id?: string; verification_milestone_name?: string; verification_operation_id?: string; verification_owner?: string; resolved_by?: string; code: string; operation: string; description: string; blocker_operation_id: string; blocker_report_id: string; blocker_revision: number }
export interface OperationWorkflowBlocker { priority?: 'low' | 'normal' | 'high' | 'urgent'; business_impact?: string; acknowledged_at?: string; acknowledged_by?: string; follow_up_at?: string; owner?: string; next_action?: string; first_observed_at?: string; code: string; description: string; operation: string }
export interface OperationWorkflowEvidenceMilestone { id: string; name: string }
export interface OperationWorkflowDependency { subject_type: string; subject_id: string; workflow: string; instance_id: string; required_outcome_code?: string }
export interface OperationWorkflowRelatedInstance { dependency: OperationWorkflowDependency; status: 'unknown' | 'waiting' | 'terminal' | 'satisfied' | 'outcome_mismatch'; state?: OperationWorkflowState }
export interface OperationWorkflowState { change_reason?: import("./customer-operation-decisions.js").OperationWorkflowChangeReason; selected_paths?: import('./customer-operation-workflow-states.js').OperationWorkflowPathSelection[]; sla?: OperationWorkflowStateSLA; depends_on?: OperationWorkflowDependency[]; dependencies_only?: boolean; outcome_code?: string; outcome_description?: string; outcome_only?: boolean; deadline_at?: string; deadline_only?: boolean; overdue: boolean; overdue_seconds?: number; report_id?: string; operation_id?: string; blocker_resolutions?: OperationWorkflowBlockerResolution[]; blockers?: OperationWorkflowBlocker[]; blockers_only?: boolean; workflow: string; instance_id: string; state: string; terminal: boolean; stale: boolean; occurred_at: string; stale_after_seconds?: number; revision: number; contract_version: number; evidence_milestones?: OperationWorkflowEvidenceMilestone[]; updated_at: string; platform_tenant_id?: string }
export interface OperationWorkflowStateHistoryEntry { outcome_link?: OperationWorkflowOutcomeLink; change_reason?: import("./customer-operation-decisions.js").OperationWorkflowChangeReason; comparison?: OperationWorkflowRevisionComparison; selected_paths?: import('./customer-operation-workflow-states.js').OperationWorkflowPathSelection[]; resolution_verifications?: OperationWorkflowResolutionVerification[]; depends_on?: OperationWorkflowDependency[]; dependencies_only?: boolean; outcome_code?: string; outcome_description?: string; outcome_only?: boolean; deadline_at?: string; deadline_only?: boolean; blocker_resolutions?: OperationWorkflowBlockerResolution[]; blockers?: OperationWorkflowBlocker[]; blockers_only?: boolean; id: string; operation_id: string; workflow: string; instance_id: string; from_state?: string; state: string; revision: number; contract_version: number; evidence_milestones?: OperationWorkflowEvidenceMilestone[]; occurred_at: string; published_at: string; platform_tenant_id?: string }
export interface OperationWorkflowInstanceMilestoneRef { id: string; operation_id: string; occurred_at: string; published_at: string }
export interface OperationWorkflowInstanceStep { step: string; label: string; operation?: string; operation_id?: string; milestone: string; position: number; observed: boolean; milestones_in_page: number; latest_milestone?: OperationWorkflowInstanceMilestoneRef; observed_in_retention: boolean; milestones_in_retention: number; latest_retained_milestone?: OperationWorkflowInstanceMilestoneRef }
export interface OperationWorkflowRetainedMilestonePath { decision?: OperationWorkflowPolicyRequirement; id: string; all_of: OperationWorkflowRetainedMilestoneRequirement[] }
export interface OperationWorkflowRetainedMilestonePathResult { decision?: OperationWorkflowPathDecisionResult; id: string; satisfied: boolean; requirements: OperationWorkflowRetainedMilestone[] }
export interface OperationWorkflowRetainedMilestoneGroup { id: string; any_of?: OperationWorkflowRetainedMilestoneRequirement[]; paths?: OperationWorkflowRetainedMilestonePath[] }
export interface OperationWorkflowRetainedMilestoneGroupResult { selected_path_id?: string; id: string; satisfied: boolean; alternatives?: OperationWorkflowRetainedMilestone[]; paths?: OperationWorkflowRetainedMilestonePathResult[] }
export interface OperationWorkflowRetainedMilestoneRequirement { operation: string; milestone: string }
export interface OperationWorkflowRetainedMilestoneFreshnessRule { prerequisite: OperationWorkflowRetainedMilestoneRequirement; max_age_seconds: number; }
export interface OperationWorkflowRetainedMilestoneFreshness { max_age_seconds: number; evaluated_at: string; expires_at?: string; age_seconds?: number; remaining_seconds?: number; }
export interface OperationWorkflowRetainedMilestoneInvalidationRule { prerequisite: OperationWorkflowRetainedMilestoneRequirement; invalidated_by: OperationWorkflowRetainedMilestoneRequirement }
export interface OperationWorkflowRetainedMilestoneInvalidation { source: OperationWorkflowRetainedMilestoneRequirement; evidence: OperationWorkflowInstanceMilestoneRef }
export interface OperationWorkflowPrerequisiteRecovery { reason: 'awaiting_verification' | 'expired' | 'invalidated'; operation: string; milestone: string; after_occurred_at?: string; steps: Array<'perform_operation' | 'publish_milestone' | 'recheck_readiness'> }
export interface OperationWorkflowRetainedMilestone { recovery?: OperationWorkflowPrerequisiteRecovery; freshness?: OperationWorkflowRetainedMilestoneFreshness; invalidation?: OperationWorkflowRetainedMilestoneInvalidation; requirement: OperationWorkflowRetainedMilestoneRequirement; status: "verified" | "awaiting_verification" | "invalidated" | "expired"; evidence?: OperationWorkflowInstanceMilestoneRef }
export interface OperationWorkflowInstanceTransition { retained_milestone_groups?: OperationWorkflowRetainedMilestoneGroup[]; retained_milestone_freshness?: OperationWorkflowRetainedMilestoneFreshnessRule[]; retained_milestone_invalidations?: OperationWorkflowRetainedMilestoneInvalidationRule[]; required_retained_milestones?: OperationWorkflowRetainedMilestoneRequirement[]; from: string; to: string; operation: string; required_dependency_workflows?: string[]; required_effects?: OperationWorkflowEffectRequirement[]; required_invariants?: OperationWorkflowInvariantRequirement[]; required_policies?: OperationWorkflowPolicyRequirement[]; required_milestones?: string[] }
export interface OperationWorkflowDecision { blockers?: OperationWorkflowBlocker[]; reason: 'state_unknown' | 'terminal' | 'no_declared_transition' | 'transitions_available' | 'state_stale' | 'application_blocked' | 'deadline_overdue' | 'dependency_waiting'; explanation: string; needs_attention: boolean; state_revision?: number; next_actions: OperationWorkflowInstanceTransition[] }
export interface OperationWorkflowInstanceSnapshot { bottlenecks?: OperationWorkflowBottlenecks; resolution_verifications?: OperationWorkflowResolutionVerification[]; awaiting_verification_count?: number; resolution_verification_count?: number; readiness?: OperationWorkflowReadinessOverview; dependency_trace?: OperationWorkflowDependencyTrace; dependency_impact?: OperationWorkflowDependencyImpact; related_workflows?: OperationWorkflowRelatedInstance[]; decision?: OperationWorkflowDecision; workflow: string; instance_id: string; contract_version: number; state?: OperationWorkflowState; steps: OperationWorkflowInstanceStep[]; allowed_transitions?: OperationWorkflowInstanceTransition[]; transitions: OperationWorkflowStateHistoryEntry[]; has_more: boolean; next_milestone_cursor?: string; next_transition_cursor?: string }
export interface OperationWorkflowOutcomeEntry { app_id: string; scope: string; platform_tenant_id?: string; subject: OperationSubject; operation_id: string; state: OperationWorkflowState }
export interface OperationWorkflowOutcomesResponse { items: OperationWorkflowOutcomeEntry[]; evaluated_at: string; next_cursor?: string }
export interface OperationWorkflowOutcomeGroup { value: string; workflow_count: number }
export interface OperationWorkflowOutcomeSummary { group_by: 'outcome' | 'workflow' | 'customer'; evaluated_at: string; workflow_count: number; groups: OperationWorkflowOutcomeGroup[]; next_cursor?: string }
export interface OperationWorkflowOutcomeOptions { appID: string; scope: string; workflow?: string; code?: string; limit?: number; cursor?: string }
export interface OperationWorkflowOutcomeSummaryOptions extends OperationWorkflowOutcomeOptions { groupBy?: 'outcome' | 'workflow' }
export interface OperationWorkflowBlockerEscalation { code: string; operation: string; owner: string; after_seconds: number; escalated_at: string }
export interface OperationWorkflowResolutionVerification { resolution: OperationWorkflowBlockerResolution; resolution_operation_id: string; resolution_report_id: string; resolution_revision: number; status: 'awaiting_verification' | 'verified'; verified_at?: string }
export interface OperationWorkflowAttentionEntry { resolution_verifications?: OperationWorkflowResolutionVerification[]; awaiting_verification_count?: number; resolution_verification_count?: number; escalations?: OperationWorkflowBlockerEscalation[]; dependency_attention?: OperationWorkflowRelatedInstance[]; app_id: string; scope: string; platform_tenant_id?: string; subject: OperationSubject; operation_id: string; state: OperationWorkflowState; reasons: Array<'blocked' | 'stale' | 'overdue' | 'dependency' | 'escalated' | 'unacknowledged' | 'follow_up_overdue' | 'awaiting_verification' | 'sla_breached' | 'sla_at_risk'> }
export interface OperationWorkflowAttentionResponse { items: OperationWorkflowAttentionEntry[]; evaluated_at: string; next_cursor?: string }
export interface OperationWorkflowAttentionStats { sla_at_risk_workflow_count: number; sla_breached_workflow_count: number; sla_unknown_workflow_count: number; awaiting_verification_workflow_count: number; awaiting_verification_resolution_count: number; low_blocker_count: number; normal_blocker_count: number; high_blocker_count: number; urgent_blocker_count: number; unacknowledged_blocker_count: number; follow_up_overdue_blocker_count: number; escalated_workflow_count: number; escalated_blocker_count: number; dependency_workflow_count: number; dependency_count: number; overdue_workflow_count: number; earliest_overdue_deadline_at?: string; longest_overdue_seconds?: number; workflow_count: number; blocked_workflow_count: number; stale_workflow_count: number; blocker_count: number; unknown_age_blockers: number; oldest_blocker_at?: string; oldest_blocker_age_seconds?: number }
export interface OperationWorkflowAttentionGroup { value: string; stats: OperationWorkflowAttentionStats }
export interface OperationWorkflowAttentionSummary { group_by: 'owner' | 'workflow' | 'blocker_code' | 'target_operation' | 'dependency_status' | 'required_outcome_code' | 'customer'; evaluated_at: string; totals: OperationWorkflowAttentionStats; groups: OperationWorkflowAttentionGroup[]; next_cursor?: string }
export interface OperationWorkflowAttentionSummaryOptions extends OperationWorkflowAttentionOptions { groupBy?: 'owner' | 'workflow' | 'blocker_code' | 'target_operation' | 'dependency_status' | 'required_outcome_code' }
export interface OperationWorkflowAttentionOptions { priority?: 'low' | 'normal' | 'high' | 'urgent'; sort?: 'updated_at' | 'deadline'; owner?: string; unassigned?: boolean; dependencyStatus?: 'waiting' | 'unknown' | 'outcome_mismatch'; requiredOutcomeCode?: string; blockerCode?: string; appID: string; scope: string; workflow?: string; targetOperation?: string; reason?: 'blocked' | 'stale' | 'overdue' | 'dependency' | 'escalated' | 'unacknowledged' | 'follow_up_overdue' | 'awaiting_verification' | 'sla_breached' | 'sla_at_risk'; limit?: number; cursor?: string }
export interface OperationMilestonePageOptions { limit?: number; cursor?: string }
export interface OperationBusinessMilestoneOptions extends OperationMilestonePageOptions { appID: string; scope: string; subjectType: string; subjectID: string; workflow?: string; workflowInstanceID?: string; workflowStateCursor?: string; staleOnly?: boolean }
export interface OperationMilestones { milestones: OperationMilestone[]; workflow_states?: OperationWorkflowState[]; workflow_state_history?: OperationWorkflowStateHistoryEntry[]; workflow_instance?: OperationWorkflowInstanceSnapshot; next_cursor?: string; next_workflow_state_cursor?: string }
export interface Operation<T = unknown> {
  subject?: OperationSubject;
  id: string; name: string; generation: number; state: OperationState;
  progress?: OperationProgress; result?: T; artifacts?: OperationArtifact[]; completion_delivery: OperationDelivery;
  cancellation_requested: boolean; failure_code?: string; waiting?: OperationWaitingState; limits?: OperationLimits; deadline_at?: string; usage?: OperationUsage; latest_sequence: number;
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
    if (response.status === 204) return undefined as T;
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
    if (options.state !== undefined && !['accepted', 'running', 'waiting', 'succeeded', 'failed', 'cancelled', 'requires_reconciliation'].includes(options.state)) throw new Error('Invalid operation state');
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
    if (options.reason !== undefined && !['blocked', 'stale', 'overdue', 'dependency', 'escalated', 'unacknowledged', 'follow_up_overdue', 'awaiting_verification'].includes(options.reason)) throw new Error('Invalid attention reason');
    const query = milestonePageQuery(options);
    query.set('app_id', options.appID); query.set('scope', options.scope);
    appendAttentionOwnerQuery(query, options);
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
    if (options.reason !== undefined && !['blocked', 'stale', 'overdue', 'dependency', 'escalated', 'unacknowledged', 'follow_up_overdue', 'awaiting_verification'].includes(options.reason)) throw new Error('Invalid attention reason');
    if (options.groupBy !== undefined && !['owner','workflow','blocker_code','target_operation','dependency_status','required_outcome_code'].includes(options.groupBy)) throw new Error('Invalid summary grouping');
    const query = milestonePageQuery(options);
    query.set('app_id', options.appID); query.set('scope', options.scope);
    appendAttentionOwnerQuery(query, options);
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
  workflowPerformanceInstances(options: OperationWorkflowPerformanceInstanceOptions, signal?: AbortSignal): Promise<OperationWorkflowPerformanceInstancesResponse> {
 if (options.baselineToken !== undefined && !['condition_gap_owner', 'condition_gap_queue'].includes(options.dimension)) throw new Error("Baseline comparisons require a performance summary or reporting-gap drilldown");
    if (['condition_gap_owner', 'condition_gap_queue'].includes(options.dimension) && (!options.baselineToken || !options.cohortToken || options.cohort !== 'ongoing')) throw new Error('Reporting-gap drilldowns require ongoing cohort, baselineToken and cohortToken');
    if (!UUID.test(options.appID) || typeof options.scope !== 'string' || !/^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$/.test(options.scope)) throw new Error('Explicit app and environment required');
    if (typeof options.workflow !== 'string' || !/^[a-z][a-z0-9-]{0,62}$/.test(options.workflow)) throw new Error('Explicit workflow required');
    if (!['completed', 'ongoing'].includes(options.cohort) || !['state_time', 'blocked_time', 'verification_wait', 'state', 'blocker', 'verification_owner', 'decision_completion', 'decision_blocked', 'condition_completion', 'condition_blocked', 'active_condition_elapsed', 'active_condition_blocked', 'condition_refresh_owner', 'condition_refresh_age', 'condition_refresh_blocked', 'condition_missing_owner', 'condition_gap_owner', 'condition_gap_queue'].includes(options.dimension)) throw new Error('Invalid performance cohort or dimension');
    const query = new URLSearchParams({ app_id: options.appID, scope: options.scope, workflow: options.workflow, cohort: options.cohort, dimension: options.dimension });
    for (const key of ['gap_status', 'contract_version', 'state', 'operation', 'code', 'owner', 'kind', 'decision_code', 'rule_id', 'rule_version', 'group_id', 'path_id', 'outcome_code', 'condition_evidence_status', 'condition_id', 'condition_description', 'condition_result'] as const) { if (options[key] !== undefined) query.set(key, String(options[key])); }
    if (options.unassigned !== undefined) query.set('unassigned', String(options.unassigned));
    if (options.cohortToken !== undefined) query.set('cohort_token', options.cohortToken);
    if (options.baselineToken !== undefined) query.set('baseline_token', options.baselineToken);
    return this.request('/v1/platform-tenant-self/workflow-performance/instances?' + query, 'GET', undefined, undefined, signal);
  }
  workflowPerformanceSummary(options: OperationWorkflowPerformanceOptions, signal?: AbortSignal): Promise<OperationWorkflowPerformanceSummary> {
    if (!UUID.test(options.appID) || typeof options.scope !== 'string' || !/^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$/.test(options.scope)) throw new Error('Explicit app and environment required');
    if (typeof options.workflow !== 'string' || !/^[a-z][a-z0-9-]{0,62}$/.test(options.workflow)) throw new Error('Explicit workflow required');
    const query = new URLSearchParams({ app_id: options.appID, scope: options.scope, workflow: options.workflow });
    if (options.baselineToken !== undefined) query.set('baseline_token', options.baselineToken);
    return this.request('/v1/platform-tenant-self/workflow-performance/summary?' + query, 'GET', undefined, undefined, signal);
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

  workflowReadinessQueue(options: OperationWorkflowReadinessQueueOptions, signal?: AbortSignal): Promise<OperationWorkflowReadinessQueueResponse> {
    if (!UUID.test(options.appID) || !/^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$/.test(options.scope)) throw new Error('Explicit app and environment required');
    if (options.owner !== undefined && options.unassigned) throw new Error('Owner and unassigned are mutually exclusive');
    const query = milestonePageQuery(options);
    query.set('app_id', options.appID); query.set('scope', options.scope);
    for (const [key, value] of Object.entries({ workflow: options.workflow, target_operation: options.targetOperation, owner: options.owner, requirement: options.requirement })) {
      if (value !== undefined) query.set(key, value);
    }
    if (options.unassigned) query.set('unassigned', 'true');
    return this.request('/v1/platform-tenant-self/workflow-readiness/queue?' + query, 'GET', undefined, undefined, signal);
  }

  createWorkflowReadinessSubscription(body: OperationWorkflowReadinessSubscriptionRequest, signal?: AbortSignal): Promise<OperationWorkflowReadinessSubscription> {
    readinessSubscriptionQuery({appID: body.app_id ?? '', scope: body.scope});
    if (body.tenant_id !== undefined) throw new Error('Customer identity derives from authentication');
    if (body.owner !== undefined && body.unassigned) throw new Error('Owner and unassigned are mutually exclusive');
    return this.request('/v1/platform-tenant-self/workflow-readiness/subscriptions', 'POST', body, undefined, signal);
  }
  workflowReadinessSubscriptions(options: OperationWorkflowReadinessSubscriptionOptions, signal?: AbortSignal): Promise<OperationWorkflowReadinessSubscriptionsResponse> {
    return this.request('/v1/platform-tenant-self/workflow-readiness/subscriptions?' + readinessSubscriptionQuery(options), 'GET', undefined, undefined, signal);
  }
  deleteWorkflowReadinessSubscription(id: string, options: OperationWorkflowReadinessSubscriptionOptions, signal?: AbortSignal): Promise<void> {
    if (!UUID.test(id)) throw new Error('Valid subscription identity required');
    return this.request('/v1/platform-tenant-self/workflow-readiness/subscriptions/' + encodeURIComponent(id) + '?' + readinessSubscriptionQuery(options), 'DELETE', undefined, undefined, signal);
  }
  workflowReadinessInbox(id: string, options: OperationWorkflowReadinessSubscriptionOptions & {afterSequence?: number; limit?: number}, signal?: AbortSignal): Promise<OperationWorkflowReadinessInboxResponse> {
    if (!UUID.test(id)) throw new Error('Valid subscription identity required');
    const q = readinessSubscriptionQuery(options);
    if (options.afterSequence !== undefined) {
      if (!Number.isSafeInteger(options.afterSequence) || options.afterSequence < 0) throw new Error('Invalid inbox sequence');
      q.set('after_sequence', String(options.afterSequence));
    }
    if (options.limit !== undefined) {
      if (!Number.isSafeInteger(options.limit) || options.limit < 1 || options.limit > 100) throw new Error('Invalid inbox page size');
      q.set('limit', String(options.limit));
    }
    return this.request('/v1/platform-tenant-self/workflow-readiness/subscriptions/' + encodeURIComponent(id) + '/notifications?' + q, 'GET', undefined, undefined, signal);
  }

  updateWorkflowReadinessNotification(id: string, sequence: number, body: OperationWorkflowReadinessCoordinationRequest, options: OperationWorkflowReadinessSubscriptionOptions, signal?: AbortSignal): Promise<OperationWorkflowReadinessNotification> {
    if (!UUID.test(id) || !Number.isSafeInteger(sequence) || sequence < 1) throw new Error('Valid notification identity required');
    if (!Number.isSafeInteger(body.revision) || body.revision < 0 || body.revision >= Number.MAX_SAFE_INTEGER) throw new Error('Invalid coordination revision');
    return this.request('/v1/platform-tenant-self/workflow-readiness/subscriptions/' + encodeURIComponent(id) + '/notifications/' + sequence + '?' + readinessSubscriptionQuery(options), 'PATCH', body, undefined, signal);
  }
  checkWorkflowReadinessNotification(id: string, sequence: number, options: OperationWorkflowReadinessSubscriptionOptions, signal?: AbortSignal): Promise<OperationWorkflowReadinessNotificationCheck> {
    if (!UUID.test(id) || !Number.isSafeInteger(sequence) || sequence < 1) throw new Error('Valid notification identity required');
    return this.request('/v1/platform-tenant-self/workflow-readiness/subscriptions/' + encodeURIComponent(id) + '/notifications/' + sequence + '?' + readinessSubscriptionQuery(options), 'GET', undefined, undefined, signal);
  }

  workflowReadinessFollowUps(options: OperationWorkflowReadinessFollowUpOptions, signal?: AbortSignal): Promise<OperationWorkflowReadinessFollowUpsResponse> {
    const q = readinessSubscriptionQuery(options);
    if (options.view !== undefined) q.set('view', options.view);
    if (options.actionKind !== undefined) q.set('action_kind', options.actionKind);
    if (options.actionTrigger !== undefined) q.set('action_trigger', options.actionTrigger);
    if (options.impactPriority !== undefined) q.set('impact_priority', options.impactPriority);
    if (options.impactKind !== undefined) q.set('impact_kind', options.impactKind);
    if (options.impactCode !== undefined) q.set('impact_code', options.impactCode);
    if (options.sort !== undefined) q.set('sort', options.sort);
    if (options.reminderOnly !== undefined) q.set('reminder_only', String(options.reminderOnly));
    if (options.owner !== undefined) q.set('owner', options.owner);
    if (options.workflow !== undefined) q.set('workflow', options.workflow);
    if (options.due !== undefined) q.set('due', options.due);
    if (options.offset !== undefined) {
      if (!Number.isSafeInteger(options.offset) || options.offset < 0 || options.offset > (options.view === 'next_actions' ? 278000 : 2000)) throw new Error('Invalid follow-up offset');
      q.set('offset', String(options.offset));
    }
    if (options.limit !== undefined) {
      if (!Number.isSafeInteger(options.limit) || options.limit < 1 || options.limit > 100) throw new Error('Invalid follow-up page size');
      q.set('limit', String(options.limit));
    }
    return this.request('/v1/platform-tenant-self/workflow-readiness/follow-ups?' + q, 'GET', undefined, undefined, signal);
  }
  workflowCoordinationTimeline(options: OperationWorkflowCoordinationTimelineOptions, signal?: AbortSignal): Promise<OperationWorkflowCoordinationTimeline> {
    const q = readinessSubscriptionQuery(options);
    if (!options.subjectType || !options.subjectID || !options.workflow || !options.instanceID) throw new Error('Explicit subject and workflow instance required');
    q.set('subject_type', options.subjectType); q.set('subject_id', options.subjectID);
    q.set('workflow', options.workflow); q.set('instance_id', options.instanceID);
    if (options.offset !== undefined) {
      if (!Number.isSafeInteger(options.offset) || options.offset < 0 || options.offset > 65700) throw new Error('Invalid timeline offset');
      q.set('offset', String(options.offset));
    }
    if (options.limit !== undefined) {
      if (!Number.isSafeInteger(options.limit) || options.limit < 1 || options.limit > 100) throw new Error('Invalid timeline page size');
      q.set('limit', String(options.limit));
    }
    return this.request('/v1/platform-tenant-self/workflow-readiness/timeline?' + q, 'GET', undefined, undefined, signal);
  }
  workflowCase(options: OperationWorkflowCoordinationTimelineOptions, signal?: AbortSignal): Promise<OperationWorkflowCase> {
    const q = readinessSubscriptionQuery(options);
    if (!options.subjectType || !options.subjectID || !options.workflow || !options.instanceID) throw new Error('Explicit subject and workflow instance required');
    q.set('subject_type', options.subjectType); q.set('subject_id', options.subjectID);
    q.set('workflow', options.workflow); q.set('instance_id', options.instanceID);
    if (options.offset !== undefined) {
      if (!Number.isSafeInteger(options.offset) || options.offset < 0 || options.offset > 65700) throw new Error('Invalid timeline offset');
      q.set('offset', String(options.offset));
    }
    if (options.limit !== undefined) {
      if (!Number.isSafeInteger(options.limit) || options.limit < 1 || options.limit > 100) throw new Error('Invalid timeline page size');
      q.set('limit', String(options.limit));
    }
    return this.request('/v1/platform-tenant-self/workflow-readiness/case?' + q, 'GET', undefined, undefined, signal);
  }
  startWorkflowPlaybookProgress(options: OperationWorkflowCoordinationTimelineOptions, body: OperationWorkflowPlaybookProgressStartRequest, signal?: AbortSignal): Promise<OperationWorkflowPlaybookProgress> {
    const q = readinessSubscriptionQuery(options);
    if (options.offset !== undefined || options.limit !== undefined) throw new Error('Pagination is not accepted for checklist writes');
    if (!options.subjectType || !options.subjectID || !options.workflow || !options.instanceID) throw new Error('Explicit subject and workflow instance required');
    q.set('subject_type', options.subjectType); q.set('subject_id', options.subjectID);
    q.set('workflow', options.workflow); q.set('instance_id', options.instanceID);

    return this.request('/v1/platform-tenant-self/workflow-readiness/playbook-progress' + '?' + q, 'POST', body, undefined, signal);
  }
  updateWorkflowPlaybookProgress(options: OperationWorkflowCoordinationTimelineOptions, id: string, body: OperationWorkflowPlaybookProgressUpdateRequest, signal?: AbortSignal): Promise<OperationWorkflowPlaybookProgress> {
    const q = readinessSubscriptionQuery(options);
    if (options.offset !== undefined || options.limit !== undefined) throw new Error('Pagination is not accepted for checklist writes');
    if (!options.subjectType || !options.subjectID || !options.workflow || !options.instanceID) throw new Error('Explicit subject and workflow instance required');
    q.set('subject_type', options.subjectType); q.set('subject_id', options.subjectID);
    q.set('workflow', options.workflow); q.set('instance_id', options.instanceID);
    if (!UUID.test(id)) throw new Error('Checklist UUID required');
    return this.request('/v1/platform-tenant-self/workflow-readiness/playbook-progress' + '/' + encodeURIComponent(id) + '?' + q, 'PATCH', body, undefined, signal);
  }
  reviewWorkflowPlaybookProgress(options: OperationWorkflowCoordinationTimelineOptions, id: string, body: OperationWorkflowPlaybookReviewRequest, signal?: AbortSignal): Promise<OperationWorkflowPlaybookProgress> {
    const q = readinessSubscriptionQuery(options);
    if (options.offset !== undefined || options.limit !== undefined) throw new Error('Pagination is not accepted for review decisions');
    if (!options.subjectType || !options.subjectID || !options.workflow || !options.instanceID || !UUID.test(id)) throw new Error('Explicit case and checklist UUID required');
    q.set('subject_type', options.subjectType); q.set('subject_id', options.subjectID);
    q.set('workflow', options.workflow); q.set('instance_id', options.instanceID);
    return this.request('/v1/platform-tenant-self/workflow-readiness/playbook-progress/' + encodeURIComponent(id) + '/reviews?' + q, 'POST', body, undefined, signal);
  }
  configureWorkflowPlaybookEvidenceWatch(options: OperationWorkflowCoordinationTimelineOptions, id: string, body: OperationWorkflowPlaybookEvidenceWatchRequest, signal?: AbortSignal): Promise<OperationWorkflowPlaybookEvidenceWatch> {
    const q = readinessSubscriptionQuery(options);
    if (options.offset !== undefined || options.limit !== undefined) throw new Error('Pagination is not accepted for watch configuration');
    if (!options.subjectType || !options.subjectID || !options.workflow || !options.instanceID || !UUID.test(id)) throw new Error('Explicit case and checklist UUID required');
    q.set('subject_type', options.subjectType); q.set('subject_id', options.subjectID);
    q.set('workflow', options.workflow); q.set('instance_id', options.instanceID);
    return this.request('/v1/platform-tenant-self/workflow-readiness/playbook-progress/' + encodeURIComponent(id) + '/evidence-watch?' + q, 'POST', body, undefined, signal);
  }
  compareWorkflowReadiness(body: OperationWorkflowReadinessComparisonRequest, signal?: AbortSignal): Promise<OperationWorkflowReadinessComparisonResponse> {
    if (!UUID.test(body.app_id ?? '') || !/^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$/.test(body.scope)) throw new Error('Explicit app and environment required');
    if (body.tenant_id !== undefined) throw new Error('Customer ownership comes from authentication');
    return this.request('/v1/platform-tenant-self/workflow-readiness/compare', 'POST', body, undefined, signal);
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
  /** ADR-1194: decide an approval this customer owns on a waiting operation. */
  decideApproval<T = unknown>(id: string, step: string, generation: number, decision: 'approved' | 'rejected', comment?: string, signal?: AbortSignal): Promise<Operation<T>> {
    if (!Number.isSafeInteger(generation) || generation < 1) throw new Error('Current operation generation required');
    if (!/^[a-z][a-z0-9-]{0,63}$/.test(step)) throw new Error('Invalid approval step');
    if (comment !== undefined && new TextEncoder().encode(comment).length > 1024) throw new Error('Approval comment exceeds 1 KiB');
    return this.request(operationPath(id) + '/approvals/' + encodeURIComponent(step), 'POST', { expected_generation: generation, decision, ...(comment === undefined ? {} : { comment }) }, undefined, signal);
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

export interface OperationWorkflowReadinessRequest { selected_paths?: import('./customer-operation-workflow-states.js').OperationWorkflowPathSelection[];
 effects?: OperationWorkflowPlannedEffect[]; invariants?: OperationWorkflowPlannedInvariant[]; decisions?: OperationWorkflowPlannedDecision[];
 app_id?: string; tenant_id?: string; scope: string; subject: OperationSubject;
 workflow: string; instance_id: string; operation: string; from_state: string; to_state: string;
 milestones?: string[]; state_revision?: number; contract_version?: number;
}
export interface OperationWorkflowTransitionReadiness {
 reporting?: OperationWorkflowTransitionReporting; pending_verifications?: OperationWorkflowResolutionVerification[];
 pending_verification_count: number; pending_verifications_truncated: boolean;
 retained_milestone_groups?: OperationWorkflowRetainedMilestoneGroupResult[];
 retained_milestones?: OperationWorkflowRetainedMilestone[];
 unmet_effects?: OperationWorkflowUnmetEffect[]; unmet_invariants?: OperationWorkflowUnmetInvariant[]; invariant_blockers?: OperationWorkflowBlocker[]; missing_dependency_workflows?: string[]; missing_policies?: OperationWorkflowPolicyRequirement[];
 transition: OperationWorkflowInstanceTransition; declared: boolean; ready: boolean;
 reasons: Array<'state_unknown'|'terminal'|'transition_undeclared'|'from_state_mismatch'|'revision_mismatch'|'contract_version_mismatch'|'application_blocked'|'dependency_unmet'|'milestone_required'|'policy_evidence_required'|'dependency_required'|'invariant_evidence_required'|'effect_evidence_required'|'retained_milestone_required'|'retained_milestone_invalidated' | 'retained_milestone_expired' | 'retained_milestone_group_unmet' | 'path_selection_invalid'>;
 advisories: Array<'state_stale'|'deadline_overdue'|'condition_reports_missing'|'condition_reports_overdue'|'condition_reporting_unknown'|'verification_pending'>;
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

function appendAttentionOwnerQuery(query: URLSearchParams, options: OperationWorkflowAttentionOptions): void {
 if (options.priority !== undefined) {
  if (!['low','normal','high','urgent'].includes(options.priority)) throw new Error('Invalid priority filter');
  query.set('priority', options.priority);
 }
 if (options.sort !== undefined) {
  if (!['updated_at','deadline'].includes(options.sort)) throw new Error('Invalid queue sort');
  query.set('sort', options.sort);
 }
 if (options.unassigned !== undefined && typeof options.unassigned !== 'boolean') throw new Error('Invalid unassigned filter');
 if (options.owner !== undefined) {
  if (typeof options.owner !== 'string' || !options.owner || new TextEncoder().encode(options.owner).length > OPERATION_WORKFLOW_BLOCKER_ACTOR_BYTES || /[\uD800-\uDFFF]/u.test(options.owner) || /[\x00-\x1f\x7f]/.test(options.owner) || options.unassigned) throw new Error('Invalid or conflicting owner filter');
  query.set('owner', options.owner);
 }
 if (options.unassigned) query.set('unassigned', 'true');
}

export interface OperationWorkflowBottlenecks { evaluated_at: string; history_complete: boolean; incomplete_reasons: string[]; observed_from?: string; observed_through?: string; reports_in_window: number; history_truncated: boolean; ongoing: boolean; state_seconds: number; blocked_seconds: number; verification_wait_seconds: number; verification_unknown_start_count: number; states: OperationWorkflowStateDuration[]; blockers: OperationWorkflowBlockerDuration[]; verification_owners: OperationWorkflowVerificationDuration[]; states_truncated: boolean; blockers_truncated: boolean; verification_owners_truncated: boolean }
export interface OperationWorkflowStateDuration { contract_version: number; state: string; observed_seconds: number; observation_count: number; ongoing: boolean }
export interface OperationWorkflowBlockerDuration { contract_version: number; operation: string; code: string; owner: string; observed_seconds: number; observation_count: number; ongoing: boolean }
export interface OperationWorkflowVerificationDuration { owner: string; observed_seconds: number; resolution_count: number; pending_count: number; unknown_start_count: number }

export interface OperationWorkflowPerformanceOptions { baselineToken?: string; appID: string; scope: string; workflow: string }
export interface OperationWorkflowPerformanceSummary { new_missing_progress?: OperationWorkflowNewMissingConditionProgress; missing_progress?: OperationWorkflowMissingConditionProgress; condition_coverage?: OperationWorkflowConditionCoverage; refresh_progress?: OperationWorkflowConditionRefreshProgress; active_conditions?: OperationWorkflowActiveConditionSummary; condition_outcomes?: OperationWorkflowConditionOutcomeSummary; decision_outcomes?: OperationWorkflowDecisionOutcomeSummary; cohort_token: string; evaluated_at: string; workflow: string; cohort_limit: number; completed: OperationWorkflowPerformanceCohort; ongoing: OperationWorkflowPerformanceCohort }
export interface OperationWorkflowDurationDistribution { workflow_count: number; total_seconds: number; p50_seconds: number; p95_seconds: number }
export interface OperationWorkflowPerformanceCoverageReason { reason: string; workflow_count: number }
export interface OperationWorkflowPerformanceCohort { sla_configured_workflow_count: number; sla_evaluated_workflow_count: number; sla_breached_workflow_count: number; sla_unknown_workflow_count: number; matching_workflow_count: number; sampled_workflow_count: number; complete_history_workflow_count: number; excluded_incomplete_workflow_count: number; cohort_truncated: boolean; exclusions: OperationWorkflowPerformanceCoverageReason[]; state_time: OperationWorkflowDurationDistribution; blocked_time: OperationWorkflowDurationDistribution; verification_wait: OperationWorkflowDurationDistribution; states: OperationWorkflowStatePerformance[]; blockers: OperationWorkflowBlockerPerformance[]; verification_owners: OperationWorkflowVerificationPerformance[]; states_truncated: boolean; blockers_truncated: boolean; verification_owners_truncated: boolean }
export interface OperationWorkflowStatePerformance { sla_evaluated_visit_count: number; sla_breached_visit_count: number; contract_version: number; state: string; duration: OperationWorkflowDurationDistribution }
export interface OperationWorkflowBlockerPerformance { contract_version: number; operation: string; code: string; owner: string; duration: OperationWorkflowDurationDistribution }
export interface OperationWorkflowVerificationPerformance { owner: string; pending_resolution_count: number; duration: OperationWorkflowDurationDistribution }

export interface OperationWorkflowPerformanceGroup { gap_status?: OperationWorkflowNewMissingConditionProgressCount['status']; condition_evidence_status?: 'within_budget'|'overdue'|'no_budget'; dimension: 'state_time' | 'blocked_time' | 'verification_wait' | 'state' | 'blocker' | 'verification_owner' | 'decision_completion' | 'decision_blocked' | 'condition_completion' | 'condition_blocked' | 'active_condition_elapsed' | 'active_condition_blocked' | 'condition_refresh_owner' | 'condition_refresh_age' | 'condition_refresh_blocked' | 'condition_missing_owner' | 'condition_gap_owner' | 'condition_gap_queue'; condition_id?: string; condition_description?: string; condition_result?: 'pass'|'fail'|'unknown'; kind?: 'decision'|'path'; decision_code?: string; rule_id?: string; rule_version?: string; group_id?: string; path_id?: string; outcome_code?: string; contract_version?: number; state?: string; operation?: string; code?: string; owner?: string; unassigned?: boolean }
export interface OperationWorkflowPerformanceInstanceOptions extends OperationWorkflowPerformanceOptions, OperationWorkflowPerformanceGroup { cohort: 'completed' | 'ongoing'; cohortToken?: string }
export interface OperationWorkflowPerformanceInstance { gap_queue_priority?: OperationWorkflowGapQueuePriority; gap_progress?: OperationWorkflowNewMissingConditionProgressInstance; missing_conditions?: OperationWorkflowConditionCoverageEntry[]; missing_condition_count?: number; missing_conditions_truncated?: boolean; refresh_priority?: OperationWorkflowConditionRefreshPriority; refresh_evidence?: OperationWorkflowDecisionOutcomeEvidence[]; refresh_evidence_count?: number; refresh_evidence_truncated?: boolean; decision_evidence?: OperationWorkflowDecisionOutcomeEvidence; platform_tenant_id?: string; subject: OperationSubject; operation_id: string; state: OperationWorkflowState; observed_seconds: number; resolution_verifications: OperationWorkflowResolutionVerification[]; awaiting_verification_count: number; resolution_verification_count: number }
export interface OperationWorkflowPerformanceInstancesResponse { new_missing_progress?: OperationWorkflowNewMissingConditionProgress; condition_coverage?: OperationWorkflowConditionCoverage; active_conditions?: OperationWorkflowActiveConditionSummary; condition_outcomes?: OperationWorkflowConditionOutcomeSummary; decision_outcomes?: OperationWorkflowDecisionOutcomeSummary; evaluated_at: string; cohort_token: string; workflow: string; cohort: 'completed' | 'ongoing'; group: OperationWorkflowPerformanceGroup; matching_workflow_count: number; sampled_workflow_count: number; complete_history_workflow_count: number; excluded_incomplete_workflow_count: number; cohort_truncated: boolean; exclusions: OperationWorkflowPerformanceCoverageReason[]; duration: OperationWorkflowDurationDistribution; items: OperationWorkflowPerformanceInstance[] }

export interface OperationWorkflowStateSLA { evaluated_at: string; budget_seconds: number; warning_percent?: number; warning_at?: string; status: 'within_budget' | 'at_risk' | 'breached' | 'unknown'; history_complete: boolean; entered_at?: string; due_at?: string; elapsed_seconds?: number; remaining_seconds?: number; breached_seconds?: number }

export interface OperationWorkflowPathDecisionResult { requirement: OperationWorkflowPolicyRequirement; reported?: import("./customer-operation-decisions.js").OperationBusinessDecision; satisfied: boolean }

export interface OperationWorkflowRevisionComparison { condition_comparison?: OperationWorkflowConditionComparison; change_reason?: import("./customer-operation-decisions.js").OperationWorkflowChangeReason; status: 'compared'|'baseline_unavailable'|'revision_gap'|'contract_changed'|'ambiguous_revision'; summary: string; previous_report_id?: string; previous_operation_id?: string; previous_revision?: number; changes: OperationWorkflowRevisionChange[]; evidence: OperationWorkflowRevisionEvidence[] }
export interface OperationWorkflowRevisionChange { kind: 'state_changed'|'blocker_added'|'blocker_removed'|'blocker_updated'|'path_selected'|'path_changed'|'decision_changed'|'contract_changed'; key: string; before?: string; after?: string; explanation: string }
export interface OperationWorkflowRevisionEvidence { kind: 'milestone'|'decision'|'verification_expected'; operation_id: string; milestone_id: string; milestone?: string }

export interface OperationWorkflowOutcomeLink { status: 'linked'|'outcome_unavailable'|'history_incomplete'|'timing_inconsistent'; summary: string; outcome_report_id?: string; outcome_operation_id?: string; outcome_revision?: number; outcome_code?: string; elapsed_seconds: number; blocked_seconds: number }

export interface OperationWorkflowDecisionOutcomeSummary { rule_comparisons?: OperationWorkflowRuleComparisonSummary; matching_workflow_count: number; sampled_workflow_count: number; linked_workflow_count: number; excluded_incomplete_workflow_count: number; missing_outcome_workflow_count: number; unclassified_workflow_count: number; cohort_truncated: boolean; groups_truncated: boolean; groups: OperationWorkflowDecisionOutcomeGroup[] }
export interface OperationWorkflowDecisionOutcomeGroup { kind: 'decision'|'path'; contract_version: number; decision_code?: string; rule_id?: string; rule_version?: string; group_id?: string; path_id?: string; outcome_code: string; completion_time: OperationWorkflowDurationDistribution; blocked_time: OperationWorkflowDurationDistribution }

export interface OperationWorkflowDecisionOutcomeEvidence { condition_freshness?: OperationWorkflowConditionFreshness; evidence_age_seconds?: number; condition?: import("./customer-operation-decisions.js").OperationBusinessConditionResult; observed_condition_results?: ("pass"|"fail"|"unknown")[]; condition_comparison?: OperationWorkflowConditionComparison; report_id: string; operation_id: string; revision: number; from_state?: string; state: string; occurred_at: string; milestone_id?: string; decision?: import('./customer-operation-decisions.js').OperationBusinessDecision }

export interface OperationWorkflowRuleComparisonSummary { sampled_workflow_count: number; unattributed_workflow_count: number; comparisons_truncated: boolean; comparisons: OperationWorkflowRuleComparison[] }
export interface OperationWorkflowRuleComparison { single_version_status: 'compared'|'insufficient_data'; multi_version_workflow_count: number; eligible_multi_version_workflow_count: number; examples_truncated: boolean; multi_version_examples: OperationWorkflowRuleVersionOverlap[]; contract_version: number; decision_code: string; rule_id: string; status: 'compared'|'insufficient_data'; versions_truncated: boolean; versions: OperationWorkflowRuleVersionStatistics[] }
export interface OperationWorkflowRuleVersionStatistics { single_version?: OperationWorkflowRuleVersionCohort; rule_version: string; observed_workflow_count: number; eligible_workflow_count: number; excluded_incomplete_workflow_count: number; missing_outcome_workflow_count: number; completion_time: OperationWorkflowDurationDistribution; blocked_time: OperationWorkflowDurationDistribution; outcomes_truncated: boolean; outcomes: OperationWorkflowRuleVersionOutcome[] }
export interface OperationWorkflowRuleVersionOutcome { outcome_code: string; workflow_count: number; proportion_basis_points: number }

export interface OperationWorkflowRuleVersionCohort { eligible_workflow_count: number; completion_time: OperationWorkflowDurationDistribution; blocked_time: OperationWorkflowDurationDistribution; outcomes_truncated: boolean; outcomes: OperationWorkflowRuleVersionOutcome[] }
export interface OperationWorkflowRuleVersionOverlap { platform_tenant_id?: string; subject: OperationSubject; instance_id: string; operation_id: string; history_complete: boolean; eligible: boolean; evidence_truncated: boolean; evidence: OperationWorkflowDecisionOutcomeEvidence[] }

export interface OperationWorkflowConditionComparison { status: 'compared'|'baseline_unavailable'|'revision_gap'|'contract_changed'|'ambiguous_revision'; changes: OperationWorkflowConditionChange[]; truncated: boolean }
export interface OperationWorkflowConditionChange { source: 'reason'|'path'; group_id?: string; condition_id: string; status: 'changed'|'newly_reported'|'unreported'; before?: import('./customer-operation-decisions.js').OperationBusinessConditionResult; after?: import('./customer-operation-decisions.js').OperationBusinessConditionResult; previous_evidence?: OperationWorkflowConditionEvidence; current_evidence: OperationWorkflowConditionEvidence; explanation: string }
export interface OperationWorkflowConditionEvidence { report_id: string; operation_id: string; revision: number; milestone_id: string; decision_code: string; rule_id: string; rule_version: string; path_id?: string }

export interface OperationWorkflowConditionOutcomeSummary { matching_workflow_count: number; sampled_workflow_count: number; linked_workflow_count: number; changing_result_workflow_count: number; excluded_incomplete_workflow_count: number; missing_outcome_workflow_count: number; unclassified_workflow_count: number; excluded_condition_evidence_workflow_count: number; cohort_truncated: boolean; groups_truncated: boolean; groups: OperationWorkflowConditionOutcomeGroup[] }
export interface OperationWorkflowConditionOutcomeGroup { contract_version: number; decision_code: string; rule_id: string; rule_version: string; condition_id: string; condition_description: string; condition_result: 'pass'|'fail'|'unknown'; outcome_code: string; changing_result_workflow_count: number; completion_time: OperationWorkflowDurationDistribution; blocked_time: OperationWorkflowDurationDistribution }

export interface OperationWorkflowActiveConditionSummary { overdue_workflow_count: number; overdue_condition_count: number; refresh_owners_truncated: boolean; refresh_owners: OperationWorkflowConditionRefreshOwnerGroup[]; matching_workflow_count: number; sampled_workflow_count: number; linked_workflow_count: number; excluded_incomplete_workflow_count: number; excluded_ambiguous_workflow_count: number; excluded_condition_evidence_workflow_count: number; unclassified_workflow_count: number; cohort_truncated: boolean; groups_truncated: boolean; groups: OperationWorkflowActiveConditionGroup[] }
export interface OperationWorkflowActiveConditionGroup { within_budget_workflow_count: number; overdue_workflow_count: number; no_budget_workflow_count: number; contract_version: number; decision_code: string; rule_id: string; rule_version: string; condition_id: string; condition_description: string; condition_result: 'pass'|'fail'|'unknown'; stale_workflow_count: number; elapsed_time: OperationWorkflowDurationDistribution; blocked_time: OperationWorkflowDurationDistribution; evidence_age: OperationWorkflowDurationDistribution }

export interface OperationWorkflowConditionFreshness { refresh_owner?: string; refresh_instructions?: string; status: 'within_budget'|'overdue'|'no_budget'; evidence_max_age_seconds?: number; refresh_due_at?: string; overdue_seconds?: number }

export interface OperationWorkflowConditionRefreshOwnerGroup { owner?: string; unassigned: boolean; overdue_condition_count: number; elapsed_time: OperationWorkflowDurationDistribution; blocked_time: OperationWorkflowDurationDistribution }

export interface OperationWorkflowConditionRefreshPriority { oldest_evidence: OperationWorkflowDecisionOutcomeEvidence; oldest_evidence_age_seconds: number; blocked_seconds: number; refresh_owners: string[]; unassigned_condition_count: number; explanation: string }

export interface OperationWorkflowConditionRefreshProgress { baseline_at: string; evaluated_at: string; sampled_workflow_count: number; compared_workflow_count: number; no_baseline_workflow_count: number; no_overdue_baseline_workflow_count: number; excluded_incomplete_workflow_count: number; excluded_ambiguous_workflow_count: number; excluded_condition_evidence_workflow_count: number; newer_report_condition_count: number; statuses: OperationWorkflowConditionRefreshProgressCount[]; items: OperationWorkflowConditionRefreshProgressInstance[] }
export interface OperationWorkflowConditionRefreshProgressCount { status: 'refreshed'|'still_overdue'|'unreported'|'contract_changed'|'budget_removed'; condition_count: number }
export interface OperationWorkflowConditionRefreshProgressInstance { platform_tenant_id?: string; subject: OperationSubject; state: OperationWorkflowState; baseline_overdue_condition_count: number; newer_report_condition_count: number; statuses: OperationWorkflowConditionRefreshProgressCount[]; changes_truncated: boolean; changes: OperationWorkflowConditionRefreshProgressChange[] }
export interface OperationWorkflowConditionRefreshProgressChange { status: 'refreshed'|'still_overdue'|'unreported'|'contract_changed'|'budget_removed'; newer_report: boolean; before: OperationWorkflowDecisionOutcomeEvidence; after?: OperationWorkflowDecisionOutcomeEvidence; explanation: string }

export interface OperationWorkflowConditionCoverage {
 missing_groups: OperationWorkflowMissingConditionGroup[]; missing_groups_truncated: boolean;
 matching_workflow_count: number; sampled_workflow_count: number; cohort_truncated: boolean; compared_workflow_count: number; no_expectations_workflow_count: number;
 excluded_incomplete_workflow_count: number; excluded_ambiguous_workflow_count: number; excluded_condition_evidence_workflow_count: number;
 expected_condition_count: number; reported_condition_count: number; missing_condition_count: number; overdue_condition_count: number;
 items: OperationWorkflowConditionCoverageInstance[];
}
export interface OperationWorkflowConditionCoverageInstance {
 platform_tenant_id?: string; subject: OperationSubject; state: OperationWorkflowState;
 expected_condition_count: number; reported_condition_count: number; missing_condition_count: number; overdue_condition_count: number;
 conditions_truncated: boolean; conditions: OperationWorkflowConditionCoverageEntry[];
}
export interface OperationWorkflowConditionCoverageEntry {
 step: string; milestone: string; definition: OperationBusinessConditionDefinition; status: 'reported'|'missing'|'overdue'; evidence?: OperationWorkflowDecisionOutcomeEvidence;
}

export interface OperationWorkflowMissingConditionGroup { contract_version: number; state: string; owner?: string; unassigned: boolean; missing_condition_count: number; blocked_time: OperationWorkflowDurationDistribution }

export interface OperationWorkflowMissingConditionProgress {
 baseline_at: string; evaluated_at: string; sampled_workflow_count: number; compared_workflow_count: number;
 no_baseline_workflow_count: number; no_missing_baseline_workflow_count: number; excluded_incomplete_workflow_count: number;
 excluded_ambiguous_workflow_count: number; excluded_condition_evidence_workflow_count: number; excluded_definition_workflow_count: number;
 statuses: OperationWorkflowMissingConditionProgressCount[]; items: OperationWorkflowMissingConditionProgressInstance[];
}
export interface OperationWorkflowMissingConditionProgressCount { status: 'received_report'|'still_missing'|'expectation_changed'|'not_expected'|'contract_changed'; condition_count: number }
export interface OperationWorkflowMissingConditionProgressInstance {
 platform_tenant_id?: string; subject: OperationSubject; baseline_report: OperationWorkflowStateHistoryEntry; state: OperationWorkflowState; state_changed: boolean;
 baseline_missing_condition_count: number; statuses: OperationWorkflowMissingConditionProgressCount[]; changes_truncated: boolean; changes: OperationWorkflowMissingConditionProgressChange[];
}
export interface OperationWorkflowMissingConditionProgressChange {
 status: 'received_report'|'still_missing'|'expectation_changed'|'not_expected'|'contract_changed'; before: OperationWorkflowConditionCoverageEntry; after?: OperationWorkflowConditionCoverageEntry; explanation: string;
}

export interface OperationWorkflowNewMissingConditionProgress {
 groups: OperationWorkflowGapProgressGroup[]; groups_truncated: boolean;
 baseline_at: string; evaluated_at: string; sampled_workflow_count: number; compared_workflow_count: number;
 no_baseline_workflow_count: number; no_current_missing_workflow_count: number; excluded_incomplete_workflow_count: number;
 excluded_ambiguous_workflow_count: number; excluded_condition_evidence_workflow_count: number; excluded_definition_workflow_count: number;
 statuses: OperationWorkflowNewMissingConditionProgressCount[]; items: OperationWorkflowNewMissingConditionProgressInstance[];
}
export interface OperationWorkflowNewMissingConditionProgressCount { status: 'report_lost'|'new_expectation'|'still_missing'|'expectation_changed'|'contract_changed'; condition_count: number }
export interface OperationWorkflowNewMissingConditionProgressInstance {
 platform_tenant_id?: string; subject: OperationSubject; baseline_report: OperationWorkflowStateHistoryEntry; baseline_terminal: boolean; state: OperationWorkflowState; state_changed: boolean;
 current_missing_condition_count: number; statuses: OperationWorkflowNewMissingConditionProgressCount[]; changes_truncated: boolean; changes: OperationWorkflowNewMissingConditionProgressChange[];
}
export interface OperationWorkflowNewMissingConditionProgressChange {
 status: 'report_lost'|'new_expectation'|'still_missing'|'expectation_changed'|'contract_changed'; before?: OperationWorkflowConditionCoverageEntry; after: OperationWorkflowConditionCoverageEntry; explanation: string;
}

export interface OperationWorkflowGapProgressGroup { contract_version: number; state: string; owner?: string; unassigned: boolean; gap_status: OperationWorkflowNewMissingConditionProgressCount['status']; missing_condition_count: number; blocked_time: OperationWorkflowDurationDistribution }

export interface OperationWorkflowGapQueuePriority {
 missing_condition_count: number; statuses: OperationWorkflowNewMissingConditionProgressCount[];
 refresh_owners: string[]; unassigned_condition_count: number; guidance: OperationWorkflowGapQueueGuidance[];
 blocked_seconds: number; explanation: string;
}
export interface OperationWorkflowGapQueueGuidance { status: OperationWorkflowNewMissingConditionProgressCount['status']; expectation: OperationWorkflowConditionCoverageEntry }

export interface OperationWorkflowTransitionReporting {
 evaluated_at: string; state: string; reporting_operation_id: string;
 status: 'compared'|'unconfigured'|'terminal'|'definition_unavailable'|'incomplete'|'limit'|'ambiguous';
 expected_condition_count: number; reported_condition_count: number; missing_condition_count: number; overdue_condition_count: number;
 conditions: OperationWorkflowConditionCoverageEntry[]; conditions_truncated: boolean;
}

/** Live sample; empty planned evidence and declared requirements determine readiness. */
export interface OperationWorkflowReadinessQueueOptions { appID: string; scope: string; workflow?: string; targetOperation?: string; owner?: string; unassigned?: boolean; requirement?: string; limit?: number }
export interface OperationWorkflowReadinessQueueEntry {
 platform_tenant_id?: string; subject: OperationSubject; workflow: string; instance_id: string; operation_id: string; state_revision: number; contract_version: number;
 transition: OperationWorkflowInstanceTransition; evaluated_at: string; ready: boolean; reasons: string[]; advisories: string[]; owners: string[]; unassigned: boolean;
 blockers: OperationWorkflowBlocker[]; missing_milestones: string[]; missing_dependency_workflows: string[]; pending_verification_count: number;
 pending_verifications?: OperationWorkflowResolutionVerification[]; pending_verifications_truncated: boolean;
 reporting_status: string; missing_report_count: number; overdue_report_count: number;
}
export interface OperationWorkflowReadinessQueueResponse {
 snapshot: OperationWorkflowReadinessSnapshot; items: OperationWorkflowReadinessQueueEntry[]; sampled_at: string; evaluated_at: string;
 matching_workflow_count: number; sampled_workflow_count: number; excluded_changed_workflow_count: number; no_transition_workflow_count: number;
 declared_transition_count: number; evaluated_transition_count: number; matching_transition_count: number; ready_transition_count: number; unmet_transition_count: number;
 workflow_sample_truncated: boolean; transition_sample_truncated: boolean; has_more: boolean;
}

export interface OperationWorkflowReadinessSnapshot { version: number; query: string; captured_at: string; items: OperationWorkflowReadinessSnapshotEntry[] }
export interface OperationWorkflowReadinessEvidence { category: string; key: string; name: string; status: string; owner?: string; operation_id?: string; invalidation_operation_id?: string; fingerprint: string }
export interface OperationWorkflowReadinessEvidenceChange { kind: string; before?: OperationWorkflowReadinessEvidence; after?: OperationWorkflowReadinessEvidence }
export interface OperationWorkflowReadinessSnapshotEntry {
 evidence?: OperationWorkflowReadinessEvidence[]; evidence_complete?: boolean;
 platform_tenant_id?: string; subject: OperationSubject; workflow: string; instance_id: string; operation_id: string; state_revision: number; contract_version: number;
 operation: string; from_state: string; to_state: string; evaluated_at: string; ready: boolean; reasons: string[]; advisories: string[];
 blocker_codes: string[]; missing_milestones: string[]; missing_dependency_workflows: string[]; pending_verification_count: number;
 requirements_fingerprint: string; blockers_fingerprint: string; prerequisites_fingerprint: string; verifications_fingerprint: string; reporting_fingerprint: string;
}
export type OperationWorkflowReadinessChangeStatus = 'newly_ready' | 'newly_blocked' | 'changed' | 'unchanged' | 'state_unknown' | 'terminal' | 'transition_unavailable' | 'contract_changed' | 'baseline_ahead';
export interface OperationWorkflowReadinessComparisonRequest { app_id?: string; tenant_id?: string; scope: string; baseline: OperationWorkflowReadinessSnapshot; change?: 'newly_ready' | 'newly_blocked' | 'changed' | 'unchanged' | 'unavailable'; limit?: number }
export interface OperationWorkflowReadinessChange {
 evidence_changes: OperationWorkflowReadinessEvidenceChange[]; evidence_comparison_complete: boolean;
 baseline: OperationWorkflowReadinessSnapshotEntry; current?: OperationWorkflowReadinessQueueEntry; current_operation_id?: string; current_state?: string;
 status: OperationWorkflowReadinessChangeStatus; added_reasons: string[]; resolved_reasons: string[]; changed_categories: string[]; explanation: string;
}
export interface OperationWorkflowReadinessComparisonResponse {
 baseline_at: string; evaluated_at: string; baseline_transition_count: number; compared_transition_count: number;
 newly_ready_count: number; newly_blocked_count: number; changed_count: number; unchanged_count: number; unavailable_count: number; matching_change_count: number;
 items: OperationWorkflowReadinessChange[]; snapshot: OperationWorkflowReadinessSnapshot; has_more: boolean;
}

export interface OperationWorkflowReadinessSubscriptionOptions { appID: string; scope: string }
export interface OperationWorkflowReadinessSubscriptionRequest { next_action_digest?: OperationWorkflowNextActionDigestPolicy; business_impact_watches?: OperationWorkflowReadinessImpactWatchRequest[];
 app_id?: string; tenant_id?: string; scope: string; name: string; workflow?: string; target_operation?: string; owner?: string; unassigned?: boolean;
 events?: ('newly_ready' | 'newly_blocked' | 'reassigned' | 'unavailable')[];
}
export interface OperationWorkflowReadinessSubscription extends Omit<OperationWorkflowReadinessSubscriptionRequest, 'app_id' | 'events' | 'business_impact_watches'> {
 digest_next_at?: string; digest_evaluated_at?: string; digest_delivered_at?: string;
 business_impact_watches?: OperationWorkflowReadinessImpactWatch[];
 id: string; app_id: string; events: string[]; created_at: string; evaluated_at: string; status: 'active' | 'retrying'; watched_transition_count: number; coverage_incomplete: boolean; latest_sequence: number;
}
export interface OperationWorkflowReadinessSubscriptionsResponse { items: OperationWorkflowReadinessSubscription[] }
export interface OperationWorkflowReadinessNotification { fulfillment_review_escalation?: OperationWorkflowFulfillmentReviewEscalation; fulfillment_review_reminder?: OperationWorkflowFulfillmentReviewReminder; fulfillment_review?: OperationWorkflowObligationFulfillmentReview; obligation_reopening?: OperationWorkflowObligationReopening; target_assessment?: OperationWorkflowObligationAssessment; obligation_receipts?: OperationWorkflowObligationEvent[]; obligation_receipts_truncated?: boolean; next_action_digest?: OperationWorkflowNextActionDigest; handoff_reassignment?: OperationWorkflowReviewResponseHandoffReassignmentEvent; handoff_decline?: OperationWorkflowReviewResponseHandoffDeclineEvent; decision_review?: OperationWorkflowReviewResponseDecisionReviewEvent; review_blockage?: OperationWorkflowReviewResponseBlockageEvent; review_check_in?: OperationWorkflowReviewResponseCheckInEvent; review_response?: OperationWorkflowReviewResponse; business_impact?: OperationWorkflowReadinessImpactEvent; dependency_recovery?: OperationWorkflowPlaybookDependencyRecovery; dependency_regression?: OperationWorkflowPlaybookDependencyRegression; playbook_step?: OperationWorkflowPlaybookStepAssignment; playbook_review?: OperationWorkflowPlaybookCompletionReview; playbook_review_event?: OperationWorkflowPlaybookCompletionReviewEvent; playbook_escalation?: OperationWorkflowPlaybookOutcomeEscalation; playbook_evidence?: OperationWorkflowPlaybookEvidenceEvent; escalation_response?: OperationWorkflowReadinessEscalationResponseEvent; outcome_escalation?: OperationWorkflowReadinessOutcomeEscalation; business_outcome_watch?: OperationWorkflowReadinessBusinessOutcomeWatch; business_outcome?: OperationWorkflowReadinessBusinessOutcomeEvent; verification_watch?: OperationWorkflowReadinessVerificationWatch; verification?: OperationWorkflowReadinessVerificationEvent; reminder?: OperationWorkflowReadinessDueReminder; coordination?: OperationWorkflowReadinessCoordination; id: string; sequence: number; kind: string; created_at: string; explanation: string; change?: OperationWorkflowReadinessChange }
export interface OperationWorkflowReadinessInboxResponse { subscription: OperationWorkflowReadinessSubscription; items: OperationWorkflowReadinessNotification[]; latest_sequence: number; next_sequence: number; resync_required: boolean; has_more: boolean }
function readinessSubscriptionQuery(options: OperationWorkflowReadinessSubscriptionOptions): URLSearchParams {
 if (!UUID.test(options.appID) || !/^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$/.test(options.scope)) throw new Error('Explicit app and environment required');
 return new URLSearchParams({app_id: options.appID, scope: options.scope});
}

export interface OperationWorkflowReadinessCoordinationRequest { review_coordination?: OperationWorkflowFulfillmentReviewCoordinationRequest; reaffirmation?: OperationWorkflowObligationReaffirmationRequest; reopening?: OperationWorkflowObligationReopeningRequest; fulfillment?: OperationWorkflowObligationFulfillmentRequest; target_binding?: OperationWorkflowObligationTargetRequest; review_response?: OperationWorkflowReviewResponseRequest; completion?: OperationWorkflowReadinessCompletionRequest; revision: number; status: 'unread' | 'read' | 'acknowledged'; owner: string; note: string; follow_up_at?: string }
export interface OperationWorkflowReadinessCoordination extends Omit<OperationWorkflowReadinessCoordinationRequest, 'review_response' | 'fulfillment' | 'target_binding' | 'reopening' | 'reaffirmation' | 'review_coordination'> { fulfillment_review_escalations?: OperationWorkflowFulfillmentReviewEscalation[]; fulfillment_review_reminders?: OperationWorkflowFulfillmentReviewReminder[]; review_coordination_changes?: OperationWorkflowFulfillmentReviewCoordinationChange[]; fulfillment_reviews?: OperationWorkflowObligationFulfillmentReview[]; reaffirmations?: OperationWorkflowObligationReaffirmation[]; reopenings?: OperationWorkflowObligationReopening[]; fulfillments?: OperationWorkflowObligationFulfillment[]; target_bindings?: OperationWorkflowObligationTargetBinding[]; completion?: OperationWorkflowReadinessCompletion; updated_at: string; updated_by: string; acknowledged_at?: string; acknowledged_by?: string }
export interface OperationWorkflowReadinessHandoff { summary: string; baseline_evaluated_at?: string; comparable: boolean; limitations: string[] }
export interface OperationWorkflowReadinessNotificationCheck { next_action_digest?: OperationWorkflowNextActionDigestCheck; business_outcome?: OperationWorkflowReadinessBusinessOutcome; handoff: OperationWorkflowReadinessHandoff; notification: OperationWorkflowReadinessNotification; checked_at: string; status: string; change?: OperationWorkflowReadinessChange }

export interface OperationWorkflowReadinessFollowUpOptions extends OperationWorkflowReadinessSubscriptionOptions { view?: 'follow_ups' | 'next_actions'; actionKind?: string; actionTrigger?: 'human' | 'automatic' | 'suggested'; impactPriority?: 'normal' | 'high' | 'critical'; impactKind?: 'milestone' | 'outcome'; impactCode?: string; sort?: 'due' | 'business_impact'; reminderOnly?: boolean; owner?: string; workflow?: string; due?: 'all' | 'overdue' | 'upcoming'; offset?: number; limit?: number }
export interface OperationWorkflowReadinessFollowUp { attention_due_at?: string; review_response?: OperationWorkflowReviewResponse; business_impact?: OperationWorkflowReadinessImpactEvent; playbook_step?: OperationWorkflowPlaybookStepAssignment; playbook_escalation?: OperationWorkflowPlaybookOutcomeEscalation; outcome_escalation?: OperationWorkflowReadinessOutcomeEscalation; reminder?: OperationWorkflowReadinessDueReminder; subscription_id: string; subscription_name: string; subscription_tenant_id?: string; platform_tenant_id?: string; scope: string; notification_id: string; sequence: number; kind: string; created_at: string; coordination: OperationWorkflowReadinessCoordination; workflow?: string; instance_id?: string; subject?: OperationSubject; operation?: string; from_state?: string; to_state?: string; overdue: boolean }
export interface OperationWorkflowReadinessFollowUpsResponse { view?: 'next_actions'; next_actions?: OperationWorkflowNextActionQueueItem[]; business_impact_summary?: OperationWorkflowReadinessImpactSummary[]; due_reminder_count: number; items: OperationWorkflowReadinessFollowUp[]; evaluated_at: string; total_count: number; overdue_count: number; next_offset: number; has_more: boolean; retention_limited: boolean }

export interface OperationWorkflowReadinessDueReminder { id: string; due_at: string; created_at: string }

export interface OperationWorkflowReadinessCompletionRequest { playbook_review_after_seconds?: number; playbook_resolution?: 'expected_outcome_achieved' | 'alternative_accepted' | 'follow_up_canceled'; escalation_policy?: OperationWorkflowReadinessOutcomeEscalationPolicy; watch_outcome?: boolean; outcome_deadline_at?: string; expected_outcome_code?: string; watch_verification?: boolean; outcome: 'resolved' | 'no_longer_applicable' | 'handed_off'; note: string; evidence_operation_ids: string[] }
export interface OperationWorkflowReadinessCompletion { playbook_review_after_seconds?: number; playbook_outcome?: OperationWorkflowPlaybookCompletionOutcome; escalation_policy?: OperationWorkflowReadinessOutcomeEscalationPolicy; watch_outcome?: boolean; outcome_deadline_at?: string; expected_outcome_code?: string; business_outcome?: OperationWorkflowReadinessBusinessOutcome; watch_verification?: boolean; outcome: 'resolved' | 'no_longer_applicable' | 'handed_off'; note: string; evidence_operation_ids: string[]; completed_at: string; completed_by: string; evidence: OperationWorkflowReadinessCompletionEvidence }
export interface OperationWorkflowReadinessCompletionEvidence { checked_at: string; status: string; explanation: string; reporting_operation_id?: string; state_revision?: number; evidence_complete: boolean; remaining_reasons: string[]; pending_verification_count: number; warnings: string[] }

export interface OperationWorkflowReadinessVerificationWatch { id: string; revision: number; started_at: string; evaluated_at: string; state_revision: number; contract_version: number; pending_count_at_start: number; coverage_incomplete: boolean; items: OperationWorkflowReadinessVerificationWatchItem[] }
export interface OperationWorkflowReadinessVerificationWatchItem { blocker_active: boolean; key: string; status: 'awaiting_verification' | 'verified' | 'invalidated' | 'unavailable'; explanation: string; reporting_revision?: number; reporting_operation_id?: string; proof: OperationWorkflowResolutionVerification }
export interface OperationWorkflowReadinessVerificationChange { before_status: 'awaiting_verification' | 'verified' | 'invalidated' | 'unavailable'; after: OperationWorkflowReadinessVerificationWatchItem }
export interface OperationWorkflowReadinessVerificationEvent { watch_id: string; source_notification_id: string; source_sequence: number; checked_at: string; source: OperationWorkflowReadinessSnapshotEntry; changes: OperationWorkflowReadinessVerificationChange[] }

export interface OperationWorkflowReadinessBusinessOutcome { expected_outcome_code: string; checked_at: string; status: 'achieved' | 'pending' | 'different_outcome' | 'unknown'; explanation: string; reporting_operation_id?: string; report_id?: string; state_revision?: number; contract_version?: number; state?: string; reported_outcome_code?: string; reported_outcome_description?: string; occurred_at?: string }

export interface OperationWorkflowReadinessBusinessOutcomeWatch { escalation?: OperationWorkflowReadinessOutcomeEscalationReference; escalation_policy?: OperationWorkflowReadinessOutcomeEscalationPolicy; state_revision: number; id: string; revision: number; started_at: string; deadline_at?: string; deadline_notified: boolean; last_notified_status?: string; current: OperationWorkflowReadinessBusinessOutcome }
export interface OperationWorkflowReadinessBusinessOutcomeEvent { escalation?: OperationWorkflowReadinessOutcomeEscalationReference; watch_id: string; source_notification_id: string; source_sequence: number; source: OperationWorkflowReadinessSnapshotEntry; previous_status: string; deadline_at?: string; current: OperationWorkflowReadinessBusinessOutcome }

export interface OperationWorkflowReadinessOutcomeEscalationPolicy { acknowledge_after_seconds?: number; fallback_owner?: string; owner: string; note?: string; follow_up_after_seconds: number }
export interface OperationWorkflowReadinessOutcomeEscalationReference { notification_id: string; sequence: number; created_at: string }
export interface OperationWorkflowReadinessOutcomeEscalation { response?: OperationWorkflowReadinessEscalationResponse; watch_id: string; source_notification_id: string; source_sequence: number; current: OperationWorkflowReadinessBusinessOutcome; success_observed_at?: string }

export interface OperationWorkflowReadinessEscalationResponse { deadline_at: string; initial_owner: string; fallback_owner?: string; status: 'awaiting' | 'acknowledged' | 'closed' | 'deadline_missed'; first_acknowledged_at?: string; deadline_notified_at?: string; history: OperationWorkflowReadinessAssignmentChange[]; history_truncated: boolean }
export interface OperationWorkflowReadinessAssignmentChange { from_owner: string; to_owner: string; at: string; actor: string; reason: 'manual_assignment' | 'response_deadline_fallback' | 'step_assignment' }
export interface OperationWorkflowReadinessEscalationResponseEvent { business_impact?: OperationWorkflowReadinessImpactEvent; escalation_notification_id: string; escalation_sequence: number; source?: OperationWorkflowReadinessSnapshotEntry; playbook?: OperationWorkflowPlaybookEvidenceEvent; checked_at: string; deadline_at: string; owner: string; reassigned: boolean }

export interface OperationWorkflowCoordinationTimelineOptions extends OperationWorkflowReadinessSubscriptionOptions { subjectType: string; subjectID: string; workflow: string; instanceID: string; offset?: number; limit?: number }
export interface OperationWorkflowCoordinationTimelineEvent { playbook_progress_id?: string; id: string; at: string; kind: string; summary: string; subscription_id?: string; subscription_name?: string; subscription_tenant_id?: string; notification_id?: string; sequence?: number; actor?: string; owner?: string; status?: string; note?: string; follow_up_at?: string; expected_outcome_code?: string; reported_outcome_code?: string; state_revision?: number; reporting_operation_ids: string[] }
export interface OperationWorkflowCoordinationTimelineSource { subscription_id: string; name: string; earliest_sequence: number; latest_sequence: number; retained_count: number; gap_before: boolean; missing_during_read: boolean }
export interface OperationWorkflowCoordinationTimeline { items: OperationWorkflowCoordinationTimelineEvent[]; sources: OperationWorkflowCoordinationTimelineSource[]; subject: OperationSubject; workflow: string; instance_id: string; scope: string; platform_tenant_id?: string; evaluated_at: string; total_count: number; next_offset: number; has_more: boolean; retention_limited: boolean; sources_truncated: boolean; history_truncated: boolean; limitations: string[] }

export interface OperationWorkflowCaseAction { kind: string; summary: string; owner?: string; operation_id?: string; subscription_id?: string; sequence?: number }
export interface OperationWorkflowCase { obligation_context?: OperationWorkflowObligationContext; obligation_assessments: OperationWorkflowObligationAssessment[]; obligation_history: OperationWorkflowCaseObligationHistoryItem[]; obligation_history_count: number; obligation_history_truncated: boolean; obligation_history_limited: boolean; obligation_history_checked_at: string; next_actions: OperationWorkflowCaseNextAction[]; next_action_count: number; next_actions_truncated: boolean; playbook_progress: OperationWorkflowPlaybookProgress[]; guidance: OperationWorkflowPlaybookGuidance[]; guidance_status: string; guidance_truncated: boolean; status: string; summary: string; instance?: OperationWorkflowInstanceSnapshot; follow_ups: OperationWorkflowReadinessFollowUp[]; follow_up_count: number; overdue_follow_up_count: number; follow_ups_truncated: boolean; actions: OperationWorkflowCaseAction[]; actions_truncated: boolean; timeline: OperationWorkflowCoordinationTimeline; evidence_checked_at: string; coordination_checked_at: string; evaluated_at: string; limitations: string[] }

export interface OperationWorkflowPlaybookEvidenceRequirement { label: string; kind: 'milestone' | 'prerequisite' | 'verification'; operation: string; milestone?: string; prerequisite_operation?: string; from_state?: string; to_state?: string; group_id?: string; path_id?: string; blocker_code?: string }
export interface OperationWorkflowPlaybookEvidenceCheck { requirement: OperationWorkflowPlaybookEvidenceRequirement; status: 'satisfied' | 'missing' | 'stale' | 'invalidated' | 'unavailable'; reason: string; checked_at: string; state_revision?: number; reporting_operation_id?: string; review_suggested: boolean; evidence?: OperationWorkflowInstanceMilestoneRef; prerequisite?: OperationWorkflowRetainedMilestone }
export interface OperationWorkflowPlaybook { outcome_guidance?: OperationWorkflowPlaybookOutcomeGuidance[]; outcome_acceptance?: OperationWorkflowPlaybookOutcomeAcceptancePolicy; outcome_deadline?: OperationWorkflowPlaybookOutcomeDeadlinePolicy; expected_outcome_code?: string; evidence_requirements?: OperationWorkflowPlaybookEvidenceRequirement[]; id: string; version: number; match_kind: 'blocker' | 'missing_milestone' | 'missing_dependency' | 'pending_verification' | 'requirement'; match_code: string; summary: string; owner?: string; required_evidence?: string[]; runbook_url?: string; action_url?: string }
export interface OperationWorkflowPlaybookGuidance { playbook: OperationWorkflowPlaybook; contract_version: number; reporting_operation_id: string }

export interface OperationWorkflowPlaybookProgressStartRequest { playbook_id: string; playbook_version: number; contract_version: number; reporting_operation_id: string }
export interface OperationWorkflowPlaybookEvidence { operation_id: string; milestone_id?: string }
export interface OperationWorkflowPlaybookProgressUpdateRequest { owner?: string; outcome_code?: string; revision: number; item_index: number; status: 'pending' | 'completed'; note: string; evidence: OperationWorkflowPlaybookEvidence[] }
export interface OperationWorkflowPlaybookProgressItem { readiness?: OperationWorkflowPlaybookStepReadiness; owner?: string; due_at?: string; evidence_check?: OperationWorkflowPlaybookEvidenceCheck; label: string; status: 'pending' | 'completed'; note: string; evidence: OperationWorkflowPlaybookEvidence[]; reviewed_by?: string; reviewed_at?: string }
export interface OperationWorkflowPlaybookProgressChange { outcome_code?: string; revision: number; item_index: number; item: OperationWorkflowPlaybookProgressItem; at: string; actor: string }
export interface OperationWorkflowPlaybookProgress { outcome_follow_ups?: OperationWorkflowPlaybookOutcomeFollowUp[]; outcome_deadline_at?: string; start_state_revision?: number; business_outcome?: OperationWorkflowPlaybookBusinessOutcome; disposition?: 'active' | 'retired' | 'replaced'; latest_review?: OperationWorkflowPlaybookReview; reviews?: OperationWorkflowPlaybookReview[]; reviews_truncated?: boolean; replaces_progress_ids?: string[]; applicability?: OperationWorkflowPlaybookApplicability; id: string; revision: number; playbook: OperationWorkflowPlaybook; contract_version: number; definition_id: string; reporting_operation_id: string; items: OperationWorkflowPlaybookProgressItem[]; completed_count: number; checklist_complete: boolean; created_at: string; created_by: string; updated_at: string; history: OperationWorkflowPlaybookProgressChange[]; history_truncated: boolean }

export interface OperationWorkflowPlaybookApplicability { status: 'applicable' | 'trigger_absent' | 'terminal_reported' | 'contract_changed' | 'guidance_changed' | 'evidence_unavailable' | 'definition_unavailable' | 'evidence_incomplete'; summary: string; suggested_action: string; checked_at: string; current_contract_version?: number; state_revision?: number; reporting_operation_id?: string; definition_id?: string; definition_changed: boolean; trigger_matched: boolean; evidence_complete: boolean; remaining_item_count: number }

export interface OperationWorkflowPlaybookReviewRequest { revision: number; decision: 'continue' | 'retire' | 'replace'; reason: string; replacement_progress_id?: string; replacement_revision?: number }
export interface OperationWorkflowPlaybookReview { revision: number; decision: 'continue' | 'retire' | 'replace'; reason: string; actor: string; at: string; applicability: OperationWorkflowPlaybookApplicability; replacement_progress_id?: string; replacement_revision?: number }

export interface OperationWorkflowPlaybookEvidenceWatchRequest { watch_id?: string; subscription_id: string; progress_revision: number; revision: number; enabled: boolean; owner: string }
export interface OperationWorkflowPlaybookEvidenceWatch { dependency_states?: OperationWorkflowPlaybookDependencyState[]; step_assignments?: OperationWorkflowPlaybookStepAssignmentReference[]; outcome_deadline?: OperationWorkflowPlaybookOutcomeDeadlinePolicy; deadline_at?: string; deadline_checked_at?: string; escalation?: OperationWorkflowReadinessOutcomeEscalationReference; outcome_state_revision?: number; business_outcome?: OperationWorkflowPlaybookBusinessOutcome; id: string; progress_id: string; subscription_id: string; revision: number; status: 'active' | 'stopped'; stop_reason?: string; owner: string; platform_tenant_id?: string; subject: OperationSubject; workflow: string; instance_id: string; contract_version: number; playbook_id: string; playbook_version: number; created_at: string; updated_at: string; updated_by: string; evaluated_at: string; items: OperationWorkflowPlaybookEvidenceCheck[] }
export interface OperationWorkflowPlaybookEvidenceChange { before_status: string; before_reason: string; after: OperationWorkflowPlaybookEvidenceCheck }
export interface OperationWorkflowPlaybookEvidenceEvent { deadline_at?: string; business_outcome?: OperationWorkflowPlaybookBusinessOutcome; outcome_change?: OperationWorkflowPlaybookOutcomeChange; watch_id: string; progress_id: string; platform_tenant_id?: string; subject: OperationSubject; workflow: string; instance_id: string; contract_version: number; playbook_id: string; playbook_version: number; checked_at: string; changes: OperationWorkflowPlaybookEvidenceChange[] }

export interface OperationWorkflowPlaybookBusinessOutcome { outcome_guidance?: OperationWorkflowPlaybookOutcomeGuidance[]; guidance?: OperationWorkflowPlaybookOutcomeGuidance; acceptance_policy?: OperationWorkflowPlaybookOutcomeAcceptancePolicy; acceptance?: 'expected' | 'acceptable_alternative' | 'unexpected' | 'pending' | 'unavailable'; deadline_at?: string; deadline_reached?: boolean; status: 'pending' | 'achieved' | 'different_outcome' | 'unavailable'; reason: string; evidence: OperationWorkflowReadinessBusinessOutcome }
export interface OperationWorkflowPlaybookOutcomeChange { before_status: string; before_reason: string; before_reported_outcome_code?: string; after: OperationWorkflowPlaybookBusinessOutcome }

export interface OperationWorkflowPlaybookOutcomeDeadlinePolicy { acknowledge_after_seconds?: number; fallback_owner?: string; after_seconds: number; owner: string; follow_up_after_seconds: number; note?: string }
export interface OperationWorkflowPlaybookOutcomeEscalation { response?: OperationWorkflowReadinessEscalationResponse; watch_id: string; progress_id: string; deadline_at: string; current: OperationWorkflowPlaybookBusinessOutcome; success_observed_at?: string }

export interface OperationWorkflowPlaybookCompletionOutcome { resolution: 'expected_outcome_achieved' | 'alternative_accepted' | 'follow_up_canceled'; alignment: 'matches' | 'mismatch' | 'unavailable' | 'not_applicable'; explanation: string; observation: OperationWorkflowPlaybookBusinessOutcome }

export interface OperationWorkflowPlaybookCompletionReview { id: string; revision: number; owner: string; status: 'active' | 'expired' | 'stopped'; expires_at: string; state_revision: number; current: OperationWorkflowPlaybookBusinessOutcome }
export interface OperationWorkflowPlaybookCompletionReviewEvent { review_id: string; source_notification_id: string; source_sequence: number; resolution: 'expected_outcome_achieved' | 'alternative_accepted' | 'follow_up_canceled'; previous: OperationWorkflowPlaybookBusinessOutcome; current: OperationWorkflowPlaybookBusinessOutcome }

export interface OperationWorkflowPlaybookOutcomeAcceptancePolicy { alternative_outcome_codes: string[] }

export interface OperationWorkflowPlaybookOutcomeGuidance { follow_up_dependencies?: OperationWorkflowPlaybookStepDependency[]; follow_up_assignments?: OperationWorkflowPlaybookStepAssignmentPolicy[]; follow_up_steps?: string[]; outcome_code: string; description: string; suggested_follow_up?: string }

export interface OperationWorkflowPlaybookOutcomeFollowUp { outcome_code: string; observation: OperationWorkflowPlaybookBusinessOutcome; items: OperationWorkflowPlaybookProgressItem[]; completed_count: number; checklist_complete: boolean }

export interface OperationWorkflowPlaybookStepAssignmentPolicy { acknowledge_after_seconds?: number; fallback_owner?: string; label: string; owner: string; complete_after_seconds: number }
export interface OperationWorkflowPlaybookStepAssignmentReference { outcome_code: string; item_index: number; notification: OperationWorkflowReadinessOutcomeEscalationReference }
export interface OperationWorkflowPlaybookStepAssignment { readiness?: OperationWorkflowPlaybookStepReadiness; source_revision?: number; response?: OperationWorkflowReadinessEscalationResponse; progress_id: string; outcome_code: string; item_index: number; label: string; status: 'pending' | 'completed'; due_at: string; last_step_owner: string; manual_owner: boolean; history: OperationWorkflowReadinessAssignmentChange[]; history_truncated: boolean }

export interface OperationWorkflowPlaybookStepDependency { label: string; prerequisites: string[] }
export interface OperationWorkflowPlaybookStepReadiness { status: 'ready' | 'waiting'; waiting_for: string[]; review_suggested: boolean }

export interface OperationWorkflowPlaybookDependencyState { regression?: OperationWorkflowReadinessOutcomeEscalationReference; outcome_code: string; item_index: number; label: string; status: 'pending' | 'completed'; owner: string; readiness: OperationWorkflowPlaybookStepReadiness }
export interface OperationWorkflowPlaybookDependencyRegression { checked_at: string; reopened_prerequisites: OperationWorkflowPlaybookDependencyState[]; affected_steps: OperationWorkflowPlaybookDependencyState[] }

export interface OperationWorkflowPlaybookDependencyRecovery { checked_at: string; regression: OperationWorkflowReadinessOutcomeEscalationReference; ready_steps: OperationWorkflowPlaybookDependencyState[] }

export type OperationWorkflowReadinessImpactSummary = import('./generated/models/OperationWorkflowReadinessImpactSummary.js').OperationWorkflowReadinessImpactSummary;
export type OperationWorkflowReadinessImpactOwner = import('./generated/models/OperationWorkflowReadinessImpactOwner.js').OperationWorkflowReadinessImpactOwner;

export type OperationWorkflowReadinessImpactWatchRequest = import('./generated/models/OperationWorkflowReadinessImpactWatchRequest.js').OperationWorkflowReadinessImpactWatchRequest;
export type OperationWorkflowReadinessImpactWatch = import('./generated/models/OperationWorkflowReadinessImpactWatch.js').OperationWorkflowReadinessImpactWatch;
export type OperationWorkflowReadinessImpactEvent = import('./generated/models/OperationWorkflowReadinessImpactEvent.js').OperationWorkflowReadinessImpactEvent;

export type OperationWorkflowReviewResponseRequest = import('./generated/models/OperationWorkflowReviewResponseRequest.js').OperationWorkflowReviewResponseRequest;

export type OperationWorkflowReviewResponseLinkRequest = import('./generated/models/OperationWorkflowReviewResponseLinkRequest.js').OperationWorkflowReviewResponseLinkRequest;

export type OperationWorkflowReviewResponseLink = import('./generated/models/OperationWorkflowReviewResponseLink.js').OperationWorkflowReviewResponseLink;

export type OperationWorkflowReviewResponse = import('./generated/models/OperationWorkflowReviewResponse.js').OperationWorkflowReviewResponse;

export type OperationWorkflowReviewResponseChange = import('./generated/models/OperationWorkflowReviewResponseChange.js').OperationWorkflowReviewResponseChange;

export type OperationWorkflowReviewResponseCheckInEvent = import('./generated/models/OperationWorkflowReviewResponseCheckInEvent.js').OperationWorkflowReviewResponseCheckInEvent;

export type OperationWorkflowReviewResponseBlockageEvent = import('./generated/models/OperationWorkflowReviewResponseBlockageEvent.js').OperationWorkflowReviewResponseBlockageEvent;

export type OperationWorkflowReviewResponseHealth = import('./generated/models/OperationWorkflowReviewResponseHealth.js').OperationWorkflowReviewResponseHealth;
export type OperationWorkflowReviewResponseSourceHealth = import('./generated/models/OperationWorkflowReviewResponseSourceHealth.js').OperationWorkflowReviewResponseSourceHealth;

export type OperationWorkflowReviewResponseDecisionRequest = import('./generated/models/OperationWorkflowReviewResponseDecisionRequest.js').OperationWorkflowReviewResponseDecisionRequest;
export type OperationWorkflowReviewResponseDecision = import('./generated/models/OperationWorkflowReviewResponseDecision.js').OperationWorkflowReviewResponseDecision;

export type OperationWorkflowReviewResponseDecisionReview = import('./generated/models/OperationWorkflowReviewResponseDecisionReview.js').OperationWorkflowReviewResponseDecisionReview;
export type OperationWorkflowReviewResponseDecisionReviewEvent = import('./generated/models/OperationWorkflowReviewResponseDecisionReviewEvent.js').OperationWorkflowReviewResponseDecisionReviewEvent;

export type OperationWorkflowReviewResponseHandoffAcceptanceRequest = import('./generated/models/OperationWorkflowReviewResponseHandoffAcceptanceRequest.js').OperationWorkflowReviewResponseHandoffAcceptanceRequest;
export type OperationWorkflowReviewResponseHandoffAcceptance = import('./generated/models/OperationWorkflowReviewResponseHandoffAcceptance.js').OperationWorkflowReviewResponseHandoffAcceptance;

export type OperationWorkflowReviewResponseHandoffDeclineRequest = import('./generated/models/OperationWorkflowReviewResponseHandoffDeclineRequest.js').OperationWorkflowReviewResponseHandoffDeclineRequest;

export type OperationWorkflowReviewResponseHandoffDecline = import('./generated/models/OperationWorkflowReviewResponseHandoffDecline.js').OperationWorkflowReviewResponseHandoffDecline;

export type OperationWorkflowReviewResponseHandoffDeclineEvent = import('./generated/models/OperationWorkflowReviewResponseHandoffDeclineEvent.js').OperationWorkflowReviewResponseHandoffDeclineEvent;

export type OperationWorkflowReviewResponseHandoffReassignmentRequest = import('./generated/models/OperationWorkflowReviewResponseHandoffReassignmentRequest.js').OperationWorkflowReviewResponseHandoffReassignmentRequest;

export type OperationWorkflowReviewResponseHandoffReassignment = import('./generated/models/OperationWorkflowReviewResponseHandoffReassignment.js').OperationWorkflowReviewResponseHandoffReassignment;

export type OperationWorkflowReviewResponseHandoffReassignmentEvent = import('./generated/models/OperationWorkflowReviewResponseHandoffReassignmentEvent.js').OperationWorkflowReviewResponseHandoffReassignmentEvent;

export type OperationWorkflowReviewResponseEscalationAcknowledgmentRequest = import('./generated/models/OperationWorkflowReviewResponseEscalationAcknowledgmentRequest.js').OperationWorkflowReviewResponseEscalationAcknowledgmentRequest;
export type OperationWorkflowReviewResponseEscalationAcknowledgment = import('./generated/models/OperationWorkflowReviewResponseEscalationAcknowledgment.js').OperationWorkflowReviewResponseEscalationAcknowledgment;

export type OperationWorkflowReviewResponseEscalationCompletionRequest = import('./generated/models/OperationWorkflowReviewResponseEscalationCompletionRequest.js').OperationWorkflowReviewResponseEscalationCompletionRequest;
export type OperationWorkflowReviewResponseEscalationCompletion = import('./generated/models/OperationWorkflowReviewResponseEscalationCompletion.js').OperationWorkflowReviewResponseEscalationCompletion;

export type OperationWorkflowReviewResponseEscalationActionReviewRequest = import('./generated/models/OperationWorkflowReviewResponseEscalationActionReviewRequest.js').OperationWorkflowReviewResponseEscalationActionReviewRequest;
export type OperationWorkflowReviewResponseEscalationRevisionRequest = import('./generated/models/OperationWorkflowReviewResponseEscalationRevisionRequest.js').OperationWorkflowReviewResponseEscalationRevisionRequest;
export type OperationWorkflowReviewResponseEscalationActionReview = import('./generated/models/OperationWorkflowReviewResponseEscalationActionReview.js').OperationWorkflowReviewResponseEscalationActionReview;

export type OperationWorkflowReviewResponseScheduledReviewCancellationRequest = import('./generated/models/OperationWorkflowReviewResponseScheduledReviewCancellationRequest.js').OperationWorkflowReviewResponseScheduledReviewCancellationRequest;
export type OperationWorkflowReviewResponseScheduledReviewCancellation = import('./generated/models/OperationWorkflowReviewResponseScheduledReviewCancellation.js').OperationWorkflowReviewResponseScheduledReviewCancellation;

export type OperationWorkflowReviewResponseScheduledReviewRescheduleRequest = import('./generated/models/OperationWorkflowReviewResponseScheduledReviewRescheduleRequest.js').OperationWorkflowReviewResponseScheduledReviewRescheduleRequest;
export type OperationWorkflowReviewResponseScheduledReviewReschedule = import('./generated/models/OperationWorkflowReviewResponseScheduledReviewReschedule.js').OperationWorkflowReviewResponseScheduledReviewReschedule;

export type OperationWorkflowCaseNextAction = import('./generated/models/OperationWorkflowCaseNextAction.js').OperationWorkflowCaseNextAction;

export type OperationWorkflowNextActionQueueItem = import('./generated/models/OperationWorkflowNextActionQueueItem.js').OperationWorkflowNextActionQueueItem;
export type OperationWorkflowNextActionCase = import('./generated/models/OperationWorkflowNextActionCase.js').OperationWorkflowNextActionCase;

export type OperationWorkflowNextActionDigestPolicy = import('./generated/models/OperationWorkflowNextActionDigestPolicy.js').OperationWorkflowNextActionDigestPolicy;
export type OperationWorkflowNextActionDigest = import('./generated/models/OperationWorkflowNextActionDigest.js').OperationWorkflowNextActionDigest;

export type OperationWorkflowNextActionDigestExplanation = import('./generated/models/OperationWorkflowNextActionDigestExplanation.js').OperationWorkflowNextActionDigestExplanation;

export type OperationWorkflowNextActionDigestCheck = import('./generated/models/OperationWorkflowNextActionDigestCheck.js').OperationWorkflowNextActionDigestCheck;

export type OperationWorkflowNextActionDigestCheckItem = import('./generated/models/OperationWorkflowNextActionDigestCheckItem.js').OperationWorkflowNextActionDigestCheckItem;

export type OperationWorkflowActionObligation = import('./generated/models/OperationWorkflowActionObligation.js').OperationWorkflowActionObligation;

export type OperationWorkflowObligationEvent = import('./generated/models/OperationWorkflowObligationEvent.js').OperationWorkflowObligationEvent;

export type OperationWorkflowCaseObligationHistoryItem = import('./generated/models/OperationWorkflowCaseObligationHistoryItem.js').OperationWorkflowCaseObligationHistoryItem;

export type OperationWorkflowObligationReplacement = import('./generated/models/OperationWorkflowObligationReplacement.js').OperationWorkflowObligationReplacement;

export type OperationWorkflowObligationContext = import('./generated/models/OperationWorkflowObligationContext.js').OperationWorkflowObligationContext;

export type OperationWorkflowObligationAssessment = import('./generated/models/OperationWorkflowObligationAssessment.js').OperationWorkflowObligationAssessment;

export type OperationWorkflowObligationTransitionTarget = import('./generated/models/OperationWorkflowObligationTransitionTarget.js').OperationWorkflowObligationTransitionTarget;

export type OperationWorkflowObligationTargetBinding = import('./generated/models/OperationWorkflowObligationTargetBinding.js').OperationWorkflowObligationTargetBinding;

export type OperationWorkflowObligationTargetRequest = import('./generated/models/OperationWorkflowObligationTargetRequest.js').OperationWorkflowObligationTargetRequest;

export type OperationWorkflowObligationFulfillment = import('./generated/models/OperationWorkflowObligationFulfillment.js').OperationWorkflowObligationFulfillment;
export type OperationWorkflowObligationFulfillmentRequest = import('./generated/models/OperationWorkflowObligationFulfillmentRequest.js').OperationWorkflowObligationFulfillmentRequest;

export type OperationWorkflowObligationReopening = import('./generated/models/OperationWorkflowObligationReopening.js').OperationWorkflowObligationReopening;
export type OperationWorkflowObligationReopeningRequest = import('./generated/models/OperationWorkflowObligationReopeningRequest.js').OperationWorkflowObligationReopeningRequest;

export type OperationWorkflowObligationFulfillmentReview = import('./generated/models/OperationWorkflowObligationFulfillmentReview.js').OperationWorkflowObligationFulfillmentReview;
export type OperationWorkflowObligationReaffirmation = import('./generated/models/OperationWorkflowObligationReaffirmation.js').OperationWorkflowObligationReaffirmation;
export type OperationWorkflowObligationReaffirmationRequest = import('./generated/models/OperationWorkflowObligationReaffirmationRequest.js').OperationWorkflowObligationReaffirmationRequest;

export type OperationWorkflowFulfillmentReviewCoordinationRequest = import('./generated/models/OperationWorkflowFulfillmentReviewCoordinationRequest.js').OperationWorkflowFulfillmentReviewCoordinationRequest;
export type OperationWorkflowFulfillmentReviewCoordinationChange = import('./generated/models/OperationWorkflowFulfillmentReviewCoordinationChange.js').OperationWorkflowFulfillmentReviewCoordinationChange;

export type OperationWorkflowFulfillmentReviewReminder = import('./generated/models/OperationWorkflowFulfillmentReviewReminder.js').OperationWorkflowFulfillmentReviewReminder;

export type OperationWorkflowFulfillmentReviewEscalation = import('./generated/models/OperationWorkflowFulfillmentReviewEscalation.js').OperationWorkflowFulfillmentReviewEscalation;
