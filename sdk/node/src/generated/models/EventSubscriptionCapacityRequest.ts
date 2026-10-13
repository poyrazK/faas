/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Explicit per-subscription live-delivery ceiling. Zero restores the plan default; positive values must not exceed the current plan ceiling. Requires deploy write or admin scope and MFA.
 */
export type EventSubscriptionCapacityRequest = {
  /**
   * Maximum pending plus dispatching deliveries; zero restores the current plan default.
   */
  max_pending_deliveries: number;
};

