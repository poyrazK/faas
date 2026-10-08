/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Public application-reported reason a named target Operation must wait. Reports replace the entire prior blocker list at their state revision; these observations do not grant or enforce execution authority.
 */
export type OperationWorkflowBlocker = {
  /**
   * Optional application observation time at or before the containing report. Upgraded transactional SDKs preserve it across repeats of the same target/code until cleared. Omitted means unknown.
   */
  first_observed_at?: string;
  code: string;
  /**
   * Public UTF-8 text limited to 512 bytes without control characters.
   */
  description: string;
  /**
   * Target Operation name.
   */
  operation: string;
};

