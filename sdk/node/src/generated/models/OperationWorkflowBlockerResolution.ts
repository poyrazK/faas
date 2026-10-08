/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Explicit application explanation for clearing one prior blocker occurrence. The source must be a retained report in the same owner/business-reference/workflow-instance/contract-version boundary and must contain this target/code. The containing state report supplies the resolution identity and timestamps. A cleared list alone does not imply a resolution fact.
 */
export type OperationWorkflowBlockerResolution = {
  code: string;
  operation: string;
  /**
   * Public UTF-8 explanation limited to 512 bytes without control characters.
   */
  description: string;
  blocker_operation_id: string;
  blocker_report_id: string;
  /**
   * Source revision must precede the resolution report revision.
   */
  blocker_revision: number;
};

