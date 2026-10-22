/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Realtime Consumer Group Settlement Result.
 */
export type RealtimeConsumerGroupSettlementResult = {
  status: 'completed' | 'ready' | 'dead' | 'leased';
  lease_until?: string;
  /**
   * Earliest eligibility timestamp returned only for ready retry results.
   */
  available_at?: string;
};

