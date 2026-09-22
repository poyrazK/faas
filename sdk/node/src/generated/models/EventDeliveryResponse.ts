/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Metadata-only lifecycle projection for one event-triggered invocation.
 */
export type EventDeliveryResponse = {
  invocation_id: string;
  event_id: string;
  event_source: string;
  event_type: string;
  subscription_id?: string;
  state: 'pending' | 'dispatching' | 'completed' | 'failed' | 'cancelled' | 'dead_letter';
  attempts: number;
  last_error?: string;
  created_at: string;
  completed_at?: string | null;
};

