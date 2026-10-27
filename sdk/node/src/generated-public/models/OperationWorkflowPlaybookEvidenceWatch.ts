/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationSubject } from './OperationSubject.js';
import type { OperationWorkflowPlaybookBusinessOutcome } from './OperationWorkflowPlaybookBusinessOutcome.js';
import type { OperationWorkflowPlaybookDependencyState } from './OperationWorkflowPlaybookDependencyState.js';
import type { OperationWorkflowPlaybookEvidenceCheck } from './OperationWorkflowPlaybookEvidenceCheck.js';
import type { OperationWorkflowPlaybookOutcomeDeadlinePolicy } from './OperationWorkflowPlaybookOutcomeDeadlinePolicy.js';
import type { OperationWorkflowPlaybookStepAssignmentReference } from './OperationWorkflowPlaybookStepAssignmentReference.js';
import type { OperationWorkflowReadinessOutcomeEscalationReference } from './OperationWorkflowReadinessOutcomeEscalationReference.js';
/**
 * OperationWorkflowPlaybookEvidenceWatch. Opt-in durable checklist evidence monitoring; advisory observations preserve human reviews and application business authority.
 */
export type OperationWorkflowPlaybookEvidenceWatch = {
  /**
   * OperationWorkflowPlaybookEvidenceWatch.review_assignments. Durable delivery references for dependency review episodes, bounded per watch and retained across disable and re-enable. Further episodes require a new watch after this limit.
   */
  review_assignments?: Array<OperationWorkflowPlaybookStepAssignmentReference>;
  /**
   * OperationWorkflowPlaybookEvidenceWatch.dependency_states. Last successful human step dependency observations; enrollment and re-enrollment establish a silent baseline.
   */
  dependency_states?: Array<OperationWorkflowPlaybookDependencyState>;
  /**
   * OperationWorkflowPlaybookEvidenceWatch.step_assignments. Durable per-watch delivery references; disable and re-enable preserve deduplication even after inbox retention.
   */
  step_assignments?: Array<OperationWorkflowPlaybookStepAssignmentReference>;
  /**
   * OperationWorkflowPlaybookEvidenceWatch.outcome_deadline. Frozen application assignment policy, independent of the ordinary notification owner.
   */
  outcome_deadline?: OperationWorkflowPlaybookOutcomeDeadlinePolicy;
  /**
   * OperationWorkflowPlaybookEvidenceWatch.deadline_at. Exact pinned checklist outcome deadline.
   */
  deadline_at?: string;
  /**
   * OperationWorkflowPlaybookEvidenceWatch.deadline_checked_at. First successful due observation, or achieved due enrollment observation; retained across configuration edits. Prevents retrospective deadline notices after observed fulfillment.
   */
  deadline_checked_at?: string;
  /**
   * OperationWorkflowPlaybookEvidenceWatch.escalation. Once-delivered assigned follow-up reference. Pruned rows are not recreated by polling.
   */
  escalation?: OperationWorkflowReadinessOutcomeEscalationReference;
  /**
   * OperationWorkflowPlaybookEvidenceWatch.outcome_state_revision. Highest comparable case report observed for the pinned expectation; retained report regressions cannot establish a new result. Preserved across configuration edits and unavailable observations.
   */
  outcome_state_revision?: number;
  /**
   * OperationWorkflowPlaybookEvidenceWatch.business_outcome. Latest retained observation of the pinned expected outcome. Enabling captures a silent baseline; ongoing polling observes result changes independently of human completion.
   */
  business_outcome?: OperationWorkflowPlaybookBusinessOutcome;
  /**
   * OperationWorkflowPlaybookEvidenceWatch.id. Pinned case identity or retained watch metadata.
   */
  id: string;
  /**
   * OperationWorkflowPlaybookEvidenceWatch.progress_id. Pinned case identity or retained watch metadata.
   */
  progress_id: string;
  /**
   * OperationWorkflowPlaybookEvidenceWatch.subscription_id. Existing inbox destination in the same application, environment, and caller access boundary.
   */
  subscription_id: string;
  /**
   * OperationWorkflowPlaybookEvidenceWatch.revision. Watch configuration revision. Zero creates; polling leaves it unchanged.
   */
  revision: number;
  /**
   * OperationWorkflowPlaybookEvidenceWatch.status. Active monitoring or stopped monitoring.
   */
  status: 'active' | 'stopped';
  /**
   * OperationWorkflowPlaybookEvidenceWatch.stop_reason. Disabled or stopped because the exact checklist was closed or unavailable.
   */
  stop_reason?: string;
  /**
   * OperationWorkflowPlaybookEvidenceWatch.owner. Responsible team label assigned to new inbox deliveries; no external destination.
   */
  owner: string;
  /**
   * OperationWorkflowPlaybookEvidenceWatch.platform_tenant_id. Exact evidence customer, included only for operator watches and deliveries.
   */
  platform_tenant_id?: string;
  /**
   * OperationWorkflowPlaybookEvidenceWatch.subject. Pinned case identity or retained watch metadata.
   */
  subject: OperationSubject;
  /**
   * OperationWorkflowPlaybookEvidenceWatch.workflow. Pinned case identity or retained watch metadata.
   */
  workflow: string;
  /**
   * OperationWorkflowPlaybookEvidenceWatch.instance_id. Pinned case identity or retained watch metadata.
   */
  instance_id: string;
  /**
   * OperationWorkflowPlaybookEvidenceWatch.contract_version. Pinned case identity or retained watch metadata.
   */
  contract_version: number;
  /**
   * OperationWorkflowPlaybookEvidenceWatch.playbook_id. Pinned case identity or retained watch metadata.
   */
  playbook_id: string;
  /**
   * OperationWorkflowPlaybookEvidenceWatch.playbook_version. Pinned case identity or retained watch metadata.
   */
  playbook_version: number;
  /**
   * OperationWorkflowPlaybookEvidenceWatch.created_at. Pinned case identity or retained watch metadata.
   */
  created_at: string;
  /**
   * OperationWorkflowPlaybookEvidenceWatch.updated_at. Pinned case identity or retained watch metadata.
   */
  updated_at: string;
  /**
   * OperationWorkflowPlaybookEvidenceWatch.updated_by. Authenticated configuration actor, or system actor for automatic monitoring stop.
   */
  updated_by: string;
  /**
   * OperationWorkflowPlaybookEvidenceWatch.evaluated_at. Last successful case evidence observation clock.
   */
  evaluated_at: string;
  /**
   * OperationWorkflowPlaybookEvidenceWatch.items. Latest retained requirement observations, separate from checklist reviews.
   */
  items: Array<OperationWorkflowPlaybookEvidenceCheck>;
};

