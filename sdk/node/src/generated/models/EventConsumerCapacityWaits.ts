/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Complete counts of effective capacity waiting reasons among retained pending application recipients in one backlog snapshot. Independent of recipient pagination. Missing or incomplete evidence cannot clear capacity alerts.
 */
export type EventConsumerCapacityWaits = {
  app_id: string;
  subscription_id: string;
  observed_at: string;
  coverage: 'all_pending_application_recipients';
  complete: boolean;
  pending_recipients: number;
  consumer: number;
  app: number;
  account: number;
};

