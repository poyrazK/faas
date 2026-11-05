/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowPlaybookBusinessOutcome } from './OperationWorkflowPlaybookBusinessOutcome.js';
/**
 * OperationWorkflowPlaybookOutcomeChange. Saved before/after playbook result observation, separate from checklist review and business causation.
 */
export type OperationWorkflowPlaybookOutcomeChange = {
  /**
   * OperationWorkflowPlaybookOutcomeChange.before_status. Prior observed playbook business-result status.
   */
  before_status: string;
  /**
   * OperationWorkflowPlaybookOutcomeChange.before_reason. Prior comparison reason.
   */
  before_reason: string;
  /**
   * OperationWorkflowPlaybookOutcomeChange.before_reported_outcome_code. Prior retained reported code, including diagnostic codes when comparability was unavailable.
   */
  before_reported_outcome_code?: string;
  /**
   * OperationWorkflowPlaybookOutcomeChange.after. New exact-case result and reporting evidence.
   */
  after: OperationWorkflowPlaybookBusinessOutcome;
};

