/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Advisory handoff context for the same checked workflow observation; scheduling-time evidence is not retained.
 */
export type OperationWorkflowReadinessHandoff = {
  /**
   * OperationWorkflowReadinessHandoff.summary. Explanation of the current observation or unavailable evidence.
   */
  summary: string;
  /**
   * OperationWorkflowReadinessHandoff.baseline_evaluated_at. Original notification comparison observation time.
   */
  baseline_evaluated_at?: string;
  /**
   * OperationWorkflowReadinessHandoff.comparable. Whether current requirements can be shown without crossing a comparison boundary.
   */
  comparable: boolean;
  /**
   * OperationWorkflowReadinessHandoff.limitations. Explicit retained-evidence and advisory authority limitations.
   */
  limitations: Array<string>;
};

