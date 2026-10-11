/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Plan assignment; an empty plan_id returns the consumer to the default plan.
 */
export type AssignAPIConsumerPlanRequest = {
  plan_id?: string;
  /**
   * UTC minute the plan takes effect; omitted means the next minute.
   */
  effective_from?: string | null;
};

