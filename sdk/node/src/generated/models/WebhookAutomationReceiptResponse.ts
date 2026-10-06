/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable provider event routing decision and automation admission progress.
 */
export type WebhookAutomationReceiptResponse = {
  receipt_id: string;
  endpoint_id: string;
  provider_event_id: string;
  workflow_name: string;
  status: 'accepted' | 'ignored';
  ignored_reason?: 'automation_paused' | 'event_filtered' | 'automation_unpublished';
  duplicate: boolean;
  accepted_at: string;
  event_source: string;
  routing_status: 'pending' | 'filtered' | 'enqueued' | 'failed' | 'ignored';
  run_id?: string;
};

