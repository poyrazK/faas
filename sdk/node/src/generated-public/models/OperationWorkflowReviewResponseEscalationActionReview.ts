/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReviewResponseHealth } from './OperationWorkflowReviewResponseHealth.js';
/**
 * Most recent explicit scoped observation of immutable completion evidence. Source indices refer to the completion evidence order. A clear observation does not establish causality or business completion; observation is not atomic with concurrent application reports. History preserves prior review snapshots within existing retention bounds.
 */
export type OperationWorkflowReviewResponseEscalationActionReview = {
  completed_at: string;
  at: string;
  actor: string;
  health: OperationWorkflowReviewResponseHealth;
};

