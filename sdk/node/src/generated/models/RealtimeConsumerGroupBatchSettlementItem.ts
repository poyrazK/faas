/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Realtime Consumer Group Batch Settlement Item.
 */
export type RealtimeConsumerGroupBatchSettlementItem = {
  sequence: string;
  action: 'complete' | 'retry' | 'renew' | 'release';
  status: 'completed' | 'ready' | 'dead' | 'leased' | 'lease_lost';
  available_at?: string;
  lease_until?: string;
};

