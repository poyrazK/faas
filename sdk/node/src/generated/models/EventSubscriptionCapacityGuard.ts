/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Value preconditions checked atomically before changing subscription capacity. Both fields are required. Checks saved configuration and the current plan allowance; live usage is not reserved.
 */
export type EventSubscriptionCapacityGuard = {
  /**
   * Expected saved setting, including zero for plan default; may exceed a downgraded plan ceiling.
   */
  max_pending_deliveries: number;
  /**
   * Expected current plan consumer ceiling before applying a subscription override.
   */
  plan_max_pending_deliveries: number;
};

