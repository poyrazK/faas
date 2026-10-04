/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Canonical tenant plan entitlement snapshot used to bound retained allocations.
 */
export type GlobalRuntimeQuotaPolicy = {
  /**
   * Digest of the canonical immutable tenant entitlement snapshot.
   */
  id: string;
  plan: 'free' | 'hobby' | 'pro' | 'scale';
  concurrency: number;
  /**
   * Steady capacity plus the plan's rollout overlap grant.
   */
  peak_concurrency: number;
};

