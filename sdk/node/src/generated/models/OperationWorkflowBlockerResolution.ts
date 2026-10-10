/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Explicit application explanation for clearing one prior blocker occurrence. The source must be a retained report in the same owner/business-reference/workflow-instance/contract-version boundary and must contain this target/code. The containing state report supplies the resolution identity and timestamps. A cleared list alone does not imply a resolution fact.
 */
export type OperationWorkflowBlockerResolution = {
  /**
   * Exact expected milestone ID paired with verification_milestone_name. Missing retained evidence leaves verification pending.
   */
  verification_milestone_id?: string;
  /**
   * Expected milestone name paired with its exact ID.
   */
  verification_milestone_name?: string;
  /**
   * Optional evidence Operation ID. Defaults to the Operation containing this resolution. Evidence must match the same account/customer/app/scope/subject/workflow-instance/contract boundary.
   */
  verification_operation_id?: string;
  /**
   * Public application-assigned verification owner limited to 128 UTF-8 bytes without control characters. Requires a verification milestone. Omitted means unassigned.
   */
  verification_owner?: string;
  /**
   * Optional public application-reported resolver identifier or team, limited to 128 UTF-8 bytes without control characters. This is not verified platform identity.
   */
  resolved_by?: string;
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

