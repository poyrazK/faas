/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Comparison of a diagnostic suggestion with observed selected-app policy. No status establishes mutation safety.
 */
export type RouteAdvicePolicyAssessment = {
  /**
   * Duplicate means the matching configured payload agrees; conflict means different configured settings. Conditional, overlapping or ambiguous rules require review. Missing evidence never means policy absence.
   */
  status: 'duplicate' | 'conflict' | 'no_matching_rule' | 'review_required' | 'unavailable';
  /**
   * Stable explanation code for this suggestion's policy assessment.
   */
  reason: string;
  /**
   * Bounded matching identifiers from this report's observed policy snapshot.
   */
  rule_ids: Array<string>;
  /**
   * More matching policy identifiers exist than are returned.
   */
  truncated?: boolean;
};

