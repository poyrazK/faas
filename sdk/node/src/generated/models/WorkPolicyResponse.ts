/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Saved policy definition and revision. For stage desired settings, created_at and updated_at identify the containing immutable workload configuration revision; stage execution remains gated.
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

