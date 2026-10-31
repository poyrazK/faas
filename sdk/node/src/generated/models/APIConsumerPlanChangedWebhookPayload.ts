/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Data delivered with consumer.plan_changed when a customer switched plans with their own key. assignment_id is stable across retries; an omitted plan is the app's default plan.
 */
export type APIConsumerPlanChangedWebhookPayload = {
  assignment_id: string;
  app_id: string;
  consumer_id: string;
  external_ref: string;
  from_plan_id?: string;
  to_plan_id?: string;
  effective_from: string;
  changed_at: string;
};

