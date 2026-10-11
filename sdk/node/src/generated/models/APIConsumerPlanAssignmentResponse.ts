/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One append-only plan assignment; no plan_id is the default plan.
 */
export type APIConsumerPlanAssignmentResponse = {
  id: string;
  consumer_id: string;
  plan_id?: string;
  effective_from: string;
  created_at: string;
};

