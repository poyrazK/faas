/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One detector decision that did not become a standalone workload
 * (issue #742). Outcome is merged when the candidate collapsed into
 * the winning workload, or skipped when the detector rejected it.
 * The legacy warnings string list remains available for compatibility.
 *
 */
export type PlanDetectionWarning = {
  /**
   * Workload affected by the decision; omitted for source-wide warnings.
   */
  workload?: string;
  detector: 'compose' | 'procfile' | 'k8s' | 'render' | 'fly' | 'serverless' | 'app_yaml' | 'other';
  /**
   * Concrete source marker associated with the detector decision.
   */
  marker: string;
  /**
   * Detector priority used by the merge tiebreak.
   */
  priority: number;
  outcome: 'merged' | 'skipped';
  /**
   * Stable human-readable reason for the detector decision.
   */
  reason: string;
};

