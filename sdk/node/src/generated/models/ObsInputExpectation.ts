/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Explicit static minimum or validated fresh compute discovery count; unknown counts remain null.
 */
export type ObsInputExpectation = {
  source: 'explicit_minimum' | 'compute_discovery';
  state: 'healthy' | 'degraded' | 'unknown';
  reason?: 'discovery_unavailable' | 'discovery_missing' | 'discovery_invalid' | 'discovery_stale' | 'discovery_invalid_targets';
  /**
   * Minimum expected returned scrape targets; null when fresh discovery cannot establish a count.
   */
  minimum_targets: (number | null);
  /**
   * Omitted invalid compute registry rows; null for static minimums or unavailable discovery.
   */
  invalid_targets: (number | null);
  /**
   * Oldest original discovery sample or successful refresh time; null without validated discovery.
   */
  observed_at: (string | null);
};

