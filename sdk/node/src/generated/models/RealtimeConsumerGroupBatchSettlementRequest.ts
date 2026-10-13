/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RealtimeConsumerGroupSettlement } from './RealtimeConsumerGroupSettlement.js';
/**
 * Realtime Consumer Group Batch Settlement Request.
 */
export type RealtimeConsumerGroupBatchSettlementRequest = {
  /**
   * Optional UUID. Retains the original successful result for 24 hours for this group and principal. Reuse requires identical normalized contents, including claim tokens and ordered batch items. At most 1024 receipts per group; full capacity returns 429 before settlement. Omit item-level keys in batches. Replays require current authorization and do not settle again. Renew receipts report the original lease expiry rather than extending it again.
   */
  idempotency_key?: string;
  settlements: Array<RealtimeConsumerGroupSettlement>;
};

