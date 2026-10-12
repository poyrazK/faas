/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Realtime Consumer Group Delivery History Event.
 */
export type RealtimeConsumerGroupDeliveryHistoryEvent = {
  id: string;
  sequence: string;
  event: 'claimed' | 'released' | 'retried' | 'lease_expired' | 'dead_lettered' | 'deadline_expired' | 'invalid_deadline' | 'completed' | 'replayed';
  attempt: number;
  fencing_token: string | null;
  failure_code: string;
  recorded_at: string;
};

