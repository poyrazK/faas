/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowFulfillmentReviewEscalation } from './OperationWorkflowFulfillmentReviewEscalation.js';
import type { OperationWorkflowFulfillmentReviewReminder } from './OperationWorkflowFulfillmentReviewReminder.js';
import type { OperationWorkflowNextActionDigest } from './OperationWorkflowNextActionDigest.js';
import type { OperationWorkflowObligationAssessment } from './OperationWorkflowObligationAssessment.js';
import type { OperationWorkflowObligationEvent } from './OperationWorkflowObligationEvent.js';
import type { OperationWorkflowObligationFulfillmentReview } from './OperationWorkflowObligationFulfillmentReview.js';
import type { OperationWorkflowObligationReopening } from './OperationWorkflowObligationReopening.js';
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
import type { OperationWorkflowReadinessImpactEvent } from './OperationWorkflowReadinessImpactEvent.js';
import type { OperationWorkflowReadinessOutcomeEscalation } from './OperationWorkflowReadinessOutcomeEscalation.js';
import type { OperationWorkflowReadinessVerificationEvent } from './OperationWorkflowReadinessVerificationEvent.js';
import type { OperationWorkflowReadinessVerificationWatch } from './OperationWorkflowReadinessVerificationWatch.js';
import type { OperationWorkflowReviewResponse } from './OperationWorkflowReviewResponse.js';
import type { OperationWorkflowReviewResponseBlockageEvent } from './OperationWorkflowReviewResponseBlockageEvent.js';
import type { OperationWorkflowReviewResponseCheckInEvent } from './OperationWorkflowReviewResponseCheckInEvent.js';
import type { OperationWorkflowReviewResponseDecisionReviewEvent } from './OperationWorkflowReviewResponseDecisionReviewEvent.js';
import type { OperationWorkflowReviewResponseHandoffDeclineEvent } from './OperationWorkflowReviewResponseHandoffDeclineEvent.js';
import type { OperationWorkflowReviewResponseHandoffReassignmentEvent } from './OperationWorkflowReviewResponseHandoffReassignmentEvent.js';
/**
 * Durable advisory readiness notification containing a bounded comparison and reporting Operation identities.
 */
export type OperationWorkflowReadinessNotification = {
  fulfillment_review_escalation?: OperationWorkflowFulfillmentReviewEscalation;
  fulfillment_review_reminder?: OperationWorkflowFulfillmentReviewReminder;
  fulfillment_review?: OperationWorkflowObligationFulfillmentReview;
  obligation_reopening?: OperationWorkflowObligationReopening;
  /**
   * Bound target prerequisite change observed by the worker; fulfillment and execution remain explicit.
   */
  target_assessment?: OperationWorkflowObligationAssessment;
  /**
   * Server-written obligation-linked lifecycle receipts saved atomically with this source mutation; bounded history is not a complete audit log.
   */
  obligation_receipts?: Array<OperationWorkflowObligationEvent>;
  /**
   * Older obligation receipts have been pruned from this source record.
   */
  obligation_receipts_truncated?: boolean;
  /**
   * Immutable retained snapshots of newly qualifying or changed team actions, with links to current controls. Source records are read independently of publication.
   */
  next_action_digest?: OperationWorkflowNextActionDigest;
  /**
   * OperationWorkflowReadinessNotification.handoff_reassignment. Explicit reassignment after decline with accountable actor, reason and new acceptance requirement.
   */
  handoff_reassignment?: OperationWorkflowReviewResponseHandoffReassignmentEvent;
  /**
   * OperationWorkflowReadinessNotification.handoff_decline. Explicit immutable decline of the current handoff with accountable actor and reason.
   */
  handoff_decline?: OperationWorkflowReviewResponseHandoffDeclineEvent;
  /**
   * OperationWorkflowReadinessNotification.decision_review. Latched review requirement after the condition captured by an explicit decision changed.
   */
  decision_review?: OperationWorkflowReviewResponseDecisionReviewEvent;
  /**
   * OperationWorkflowReadinessNotification.review_blockage. Newly observed blockers or positively verified recoveries for an active response plan.
   */
  review_blockage?: OperationWorkflowReviewResponseBlockageEvent;
  /**
   * OperationWorkflowReadinessNotification.review_check_in. One-time missed response check-in with immutable plan/link/ownership context; source resolution and rescheduling fence stale publication.
   */
  review_check_in?: OperationWorkflowReviewResponseCheckInEvent;
  /**
   * OperationWorkflowReadinessNotification.review_response. Explicit human response ownership, plan and check-in linked to protected-result reviews; separate from alert acknowledgment and application execution.
   */
  review_response?: OperationWorkflowReviewResponse;
  /**
   * OperationWorkflowReadinessNotification.business_impact. Sampled critical/overdue review transitions or source-confirmed clearance of tracked obligations; never an application business outcome assertion.
   */
  business_impact?: OperationWorkflowReadinessImpactEvent;
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
  kind: 'review_response_handoff_reassigned' | 'review_response_handoff_declined' | 'fulfillment_review_required' | 'fulfillment_review_overdue' | 'fulfillment_review_escalated' | 'obligation_reopened' | 'obligation_target_changed' | 'next_action_digest' | 'review_response_escalation_action_checked' | 'review_response_escalation_commitment_overdue' | 'review_response_handoff_acceptance_escalated' | 'review_response_handoff_acceptance_overdue' | 'review_response_decision_review_overdue' | 'review_response_decision_review_required' | 'review_response_recovered' | 'review_response_blocked' | 'review_response_check_in_overdue' | 'business_impact_changed' | 'newly_ready' | 'newly_blocked' | 'reassigned' | 'unavailable' | 'verification_verified' | 'verification_invalidated' | 'verification_unavailable' | 'outcome_achieved' | 'outcome_different_outcome' | 'outcome_unavailable' | 'outcome_deadline_reached' | 'escalation_response_overdue' | 'playbook_evidence_changed' | 'playbook_outcome_changed' | 'playbook_outcome_deadline_reached' | 'playbook_outcome_step_assigned' | 'playbook_dependency_regressed' | 'playbook_dependency_recovered';
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

