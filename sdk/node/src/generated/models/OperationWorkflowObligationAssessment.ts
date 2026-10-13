/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowObligationTargetBinding } from './OperationWorkflowObligationTargetBinding.js';
import type { OperationWorkflowTransitionReadiness } from './OperationWorkflowTransitionReadiness.js';
/**
 * Coordination assessment for one displayed outstanding source action. Human reviews, ownership acceptance and worker checks are distinct from executing application transitions. Free-form response/coordinator plans remain fulfillment_unassessed without a declared binding. Deadlines and histories never create dependency guards or authorize actions.
 */
export type OperationWorkflowObligationAssessment = {
  binding?: OperationWorkflowObligationTargetBinding;
  readiness?: OperationWorkflowTransitionReadiness;
  subscription_id: string;
  sequence: number;
  obligation_id?: string;
  action_kind: string;
  status: 'fulfillment_unassessed' | 'acceptance_required' | 'scheduled_check' | 'review_actionable' | 'review_evidence_unavailable' | 'human_action_required' | 'target_unavailable' | 'waiting_on_target_requirements' | 'target_requirements_satisfied';
  summary: string;
  checked_at: string;
};

