/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowRecoveryAutomaticAction } from './WorkflowRecoveryAutomaticAction.js';
/**
 * Automatic retry execution records for the frozen review plan. Manual application is recorded through the existing keyed child receipts and is not charged to automatic execution budgets.
 */
export type WorkflowRecoveryReviewAutomaticExecution = {
  actions: Array<WorkflowRecoveryAutomaticAction>;
};

