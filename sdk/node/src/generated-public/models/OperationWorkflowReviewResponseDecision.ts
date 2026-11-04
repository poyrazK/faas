/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReviewResponseHealth } from './OperationWorkflowReviewResponseHealth.js';
/**
 * Immutable human decision with accountable actor and the reviewed health snapshot. Application execution remains application-owned.
 */
export type OperationWorkflowReviewResponseDecision = {
  /**
   * OperationWorkflowReviewResponseDecision.kind. Recorded human intent.
   */
  kind: 'continue' | 'revise' | 'pause';
  /**
   * OperationWorkflowReviewResponseDecision.reason. Operator explanation.
   */
  reason: string;
  /**
   * OperationWorkflowReviewResponseDecision.owner. Responsible team at decision time.
   */
  owner: string;
  /**
   * OperationWorkflowReviewResponseDecision.at. Server decision time.
   */
  at: string;
  /**
   * OperationWorkflowReviewResponseDecision.actor. Authenticated operator who saved the decision.
   */
  actor: string;
  /**
   * OperationWorkflowReviewResponseDecision.health. Immutable reviewed snapshot including original freshness timestamps.
   */
  health: OperationWorkflowReviewResponseHealth;
};

