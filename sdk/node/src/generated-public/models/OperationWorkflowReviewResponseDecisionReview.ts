/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReviewResponseEscalationAcknowledgment } from './OperationWorkflowReviewResponseEscalationAcknowledgment.js';
import type { OperationWorkflowReviewResponseHandoffAcceptance } from './OperationWorkflowReviewResponseHandoffAcceptance.js';
import type { OperationWorkflowReviewResponseHandoffDecline } from './OperationWorkflowReviewResponseHandoffDecline.js';
import type { OperationWorkflowReviewResponseHandoffReassignment } from './OperationWorkflowReviewResponseHandoffReassignment.js';
import type { OperationWorkflowReviewResponseHealth } from './OperationWorkflowReviewResponseHealth.js';
/**
 * First changed observation for one explicit response decision. Remains latched until a new explicit decision; ordinary edits, acknowledgment and subsequent recovery do not clear it.
 */
export type OperationWorkflowReviewResponseDecisionReview = {
  /**
   * OperationWorkflowReviewResponseDecisionReview.escalation_acknowledgment. Explicit escalation coordination commitment, separate from handoff acceptance and decision revalidation.
   */
  escalation_acknowledgment?: OperationWorkflowReviewResponseEscalationAcknowledgment;
  /**
   * OperationWorkflowReviewResponseDecisionReview.acceptance_escalation_owner. Escalation destination pinned at the actual handoff. Missing legacy destinations are not retroactively assigned; a destination equal to current ownership receives only the ordinary overdue notice.
   */
  acceptance_escalation_owner?: string;
  /**
   * OperationWorkflowReviewResponseDecisionReview.acceptance_escalated_at. One-time escalation delivery marker committed atomically with the overdue marker and both inbox notices. Reset only by a new handoff.
   */
  acceptance_escalated_at?: string;
  /**
   * OperationWorkflowReviewResponseDecisionReview.handed_off_at. Current handoff timestamp; required by acceptance and decline after manual reassignment. Legacy automatic handoffs can omit it.
   */
  handed_off_at?: string;
  /**
   * OperationWorkflowReviewResponseDecisionReview.reassignment. Explicit reassignment after decline with accountable actor, reason and new acceptance requirement.
   */
  reassignment?: OperationWorkflowReviewResponseHandoffReassignment;
  /**
   * OperationWorkflowReviewResponseDecisionReview.reassignment_required. Declining team still owns the unresolved review; manual owner reassignment clears this flag without erasing decline history.
   */
  reassignment_required?: boolean;
  /**
   * OperationWorkflowReviewResponseDecisionReview.decline. Explicit immutable decline of the current handoff with accountable actor and reason.
   */
  decline?: OperationWorkflowReviewResponseHandoffDecline;
  /**
   * OperationWorkflowReviewResponseDecisionReview.acceptance_due_at. Acceptance deadline pinned from the actual handoff time and effective window, separate from the unchanged decision-review deadline.
   */
  acceptance_due_at?: string;
  /**
   * OperationWorkflowReviewResponseDecisionReview.acceptance_deadline_notified_at. One-time unaccepted handoff alert marker, independent of notification acknowledgment.
   */
  acceptance_deadline_notified_at?: string;
  /**
   * OperationWorkflowReviewResponseDecisionReview.acceptance. Explicit acceptance for the current handed-off review, separate from notification acknowledgment and decision revalidation.
   */
  acceptance?: OperationWorkflowReviewResponseHandoffAcceptance;
  /**
   * OperationWorkflowReviewResponseDecisionReview.initial_owner. Team responsible when this review episode began; unknown legacy ownership prevents automatic handoff.
   */
  initial_owner?: string;
  /**
   * OperationWorkflowReviewResponseDecisionReview.owner_changed. Ownership changed during this episode, including a move away and back; prevents automatic handoff.
   */
  owner_changed?: boolean;
  /**
   * OperationWorkflowReviewResponseDecisionReview.fallback_owner. Distinct fallback team pinned when this episode began; future policy edits do not rewrite it.
   */
  fallback_owner?: string;
  /**
   * OperationWorkflowReviewResponseDecisionReview.previous_owner. Responsible team immediately before the deadline evaluation.
   */
  previous_owner?: string;
  /**
   * OperationWorkflowReviewResponseDecisionReview.owner. Responsible team after the deadline evaluation, whether handoff occurred or was protected.
   */
  owner?: string;
  /**
   * OperationWorkflowReviewResponseDecisionReview.reassigned. Whether the overdue evaluation transferred both response and inbox ownership.
   */
  reassigned?: boolean;
  /**
   * OperationWorkflowReviewResponseDecisionReview.due_at. Immutable deadline pinned from required_at and the response review window; legacy flags are backfilled.
   */
  due_at?: string;
  /**
   * OperationWorkflowReviewResponseDecisionReview.deadline_notified_at. One-time overdue alert marker; acknowledgment and ordinary edits do not fulfill revalidation.
   */
  deadline_notified_at?: string;
  /**
   * OperationWorkflowReviewResponseDecisionReview.decision_at. Time of the decision that needs revalidation.
   */
  decision_at: string;
  /**
   * OperationWorkflowReviewResponseDecisionReview.required_at. First sampled condition change, committed with its one-time inbox alert.
   */
  required_at: string;
  /**
   * OperationWorkflowReviewResponseDecisionReview.changes. Differences from the immutable reviewed snapshot, including unavailable sources without claiming business failure.
   */
  changes: Array<string>;
  /**
   * OperationWorkflowReviewResponseDecisionReview.health. Frozen first changed health observation; live response health may continue changing.
   */
  health: OperationWorkflowReviewResponseHealth;
};

