/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Saved app policy and revision used for subsequent work admissions.
 */
export type WorkPolicyResponse = {
  name: string;
  revision: number;
  max_running_per_key: 1;
  max_running_per_fairness_key: number;
  pending_updates: 'all' | 'keep_latest';
  debounce_ms: number;
  expires_after_ms: number;
  created_at: string;
  updated_at: string;
};

