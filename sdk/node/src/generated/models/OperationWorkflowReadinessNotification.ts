/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowPlaybookCompletionReview } from './OperationWorkflowPlaybookCompletionReview.js';
import type { OperationWorkflowPlaybookCompletionReviewEvent } from './OperationWorkflowPlaybookCompletionReviewEvent.js';
import type { OperationWorkflowPlaybookDependencyRecovery } from './OperationWorkflowPlaybookDependencyRecovery.js';
import type { OperationWorkflowPlaybookDependencyRegression } from './OperationWorkflowPlaybookDependencyRegression.js';
import type { OperationWorkflowPlaybookEvidenceEvent } from './OperationWorkflowPlaybookEvidenceEvent.js';
import type { OperationWorkflowPlaybookOutcomeEscalation } from './OperationWorkflowPlaybookOutcomeEscalation.js';
import type { OperationWorkflowPlaybookStepAssignment } from './OperationWorkflowPlaybookStepAssignment.js';
import type { OperationWorkflowReadinessBusinessOutcomeEvent } from './OperationWorkflowReadinessBusinessOutcomeEvent.js';
import type { OperationWorkflowReadinessBusinessOutcomeWatch } from './OperationWorkflowReadinessBusinessOutcomeWatch.js';
import type { OperationWorkflowReadinessChange } from './OperationWorkflowReadinessChange.js';
import type { OperationWorkflowReadinessCoordination } from './OperationWorkflowReadinessCoordination.js';
import type { OperationWorkflowReadinessDueReminder } from './OperationWorkflowReadinessDueReminder.js';
import type { OperationWorkflowReadinessEscalationResponseEvent } from './OperationWorkflowReadinessEscalationResponseEvent.js';
import type { OperationWorkflowReadinessOutcomeEscalation } from './OperationWorkflowReadinessOutcomeEscalation.js';
import type { OperationWorkflowReadinessVerificationEvent } from './OperationWorkflowReadinessVerificationEvent.js';
import type { OperationWorkflowReadinessVerificationWatch } from './OperationWorkflowReadinessVerificationWatch.js';
/**
 * Durable advisory readiness notification containing a bounded comparison and reporting Operation identities.
 */
export type OperationWorkflowReadinessNotification = {
  dependency_recovery?: OperationWorkflowPlaybookDependencyRecovery;
  dependency_regression?: OperationWorkflowPlaybookDependencyRegression;
  playbook_step?: OperationWorkflowPlaybookStepAssignment;
  playbook_review?: OperationWorkflowPlaybookCompletionReview;
  playbook_review_event?: OperationWorkflowPlaybookCompletionReviewEvent;
  /**
   * OperationWorkflowReadinessNotification.playbook_escalation. Live evidence of an assigned playbook deadline follow-up, separate from human coordination and immutable delivery evidence.
   */
  playbook_escalation?: OperationWorkflowPlaybookOutcomeEscalation;
  /**
   * OperationWorkflowReadinessNotification.playbook_evidence. Durable opt-in checklist evidence monitoring, independent of readiness discovery filters.
   */
  playbook_evidence?: OperationWorkflowPlaybookEvidenceEvent;
  escalation_response?: OperationWorkflowReadinessEscalationResponseEvent;
  outcome_escalation?: OperationWorkflowReadinessOutcomeEscalation;
  business_outcome_watch?: OperationWorkflowReadinessBusinessOutcomeWatch;
  business_outcome?: OperationWorkflowReadinessBusinessOutcomeEvent;
  verification_watch?: OperationWorkflowReadinessVerificationWatch;
  verification?: OperationWorkflowReadinessVerificationEvent;
  coordination?: OperationWorkflowReadinessCoordination;
  /**
   * OperationWorkflowReadinessNotification.id. Stable notification UUID.
   */
  id: string;
  /**
   * OperationWorkflowReadinessNotification.sequence. Monotonic subscription-local delivery cursor.
   */
  sequence: number;
  /**
   * OperationWorkflowReadinessNotification.kind. Matched subscription notification kind.
   */
  kind: 'newly_ready' | 'newly_blocked' | 'reassigned' | 'unavailable' | 'verification_verified' | 'verification_invalidated' | 'verification_unavailable' | 'outcome_achieved' | 'outcome_different_outcome' | 'outcome_unavailable' | 'outcome_deadline_reached' | 'escalation_response_overdue' | 'playbook_evidence_changed' | 'playbook_outcome_changed' | 'playbook_outcome_deadline_reached' | 'playbook_outcome_step_assigned' | 'playbook_dependency_regressed' | 'playbook_dependency_recovered';
  /**
   * OperationWorkflowReadinessNotification.created_at. Time the observation was converted into a notification.
   */
  created_at: string;
  /**
   * OperationWorkflowReadinessNotification.explanation. Human-readable change or unavailable-evaluation explanation.
   */
  explanation: string;
  change?: OperationWorkflowReadinessChange;
  reminder?: OperationWorkflowReadinessDueReminder;
};

