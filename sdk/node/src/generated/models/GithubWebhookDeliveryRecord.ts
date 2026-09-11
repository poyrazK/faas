/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Payload-free queue metadata for one inbound GitHub webhook delivery.
 */
export type GithubWebhookDeliveryRecord = {
  delivery_id: string;
  event_type: string;
  status: 'pending' | 'processing' | 'succeeded' | 'dead';
  attempts: number;
  next_attempt_at: string;
  last_error?: string;
  received_at: string;
  processed_at?: string | null;
  updated_at: string;
};

