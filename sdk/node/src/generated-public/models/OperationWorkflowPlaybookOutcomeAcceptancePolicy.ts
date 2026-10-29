/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * OperationWorkflowPlaybookOutcomeAcceptancePolicy. Explicit business acceptance rules pinned with the playbook. Requires an expected outcome; an empty list permits no alternatives. Omission preserves legacy reviewer acceptance.
 */
export type OperationWorkflowPlaybookOutcomeAcceptancePolicy = {
  /**
   * OperationWorkflowPlaybookOutcomeAcceptancePolicy.alternative_outcome_codes. Distinct application-defined terminal outcome slugs, excluding the primary expected outcome. Sorted canonically; never overrides application reports or the primary deadline target.
   */
  alternative_outcome_codes: Array<string>;
};

