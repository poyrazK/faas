/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Coverage of the bounded policy-independent terminal-failure sample in the requested workflow/time window, before policy filters. Counts are sample counts; partial capture and pruning never establish that no other failures occurred. Review correlation uses the sampled reviews.
 */
export type WorkflowRecoveryFailureCoverage = {
  capture_started_at: string;
  /**
   * Prospective capture and retention limits prevent a claim of complete historical coverage.
   */
  coverage: 'partial';
  captured_failures: number;
  reviewed_failures: number;
  unreviewed_failures: number;
  /**
   * Sampled failures without historical retry preflight proof.
   */
  missing_eligibility_evidence: number;
  truncated: boolean;
};

