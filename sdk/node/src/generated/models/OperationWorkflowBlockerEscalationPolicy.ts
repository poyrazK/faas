/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Application-declared recommendation to escalate a blocker once its known age reaches a threshold. Does not assign work or execute actions.
 */
export type OperationWorkflowBlockerEscalationPolicy = {
  /**
   * Whole seconds from first_observed_at, bounded to ten years.
   */
  after_seconds: number;
  /**
   * Public recommended escalation team or person identifier, limited to 128 UTF-8 bytes without control characters.
   */
  owner: string;
};

