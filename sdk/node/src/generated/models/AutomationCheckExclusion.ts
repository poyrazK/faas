/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * An explicitly reviewed coverage exclusion. Reasons are user-authored metadata and should contain no secrets.
 */
export type AutomationCheckExclusion = {
  step: string;
  loop?: string;
  code: 'guard_match_missing' | 'guard_skip_missing' | 'failure_route_missing' | 'wait_success_missing' | 'wait_timeout_missing' | 'retry_missing' | 'loop_empty_missing' | 'loop_multiple_items_missing';
  reason: string;
};

