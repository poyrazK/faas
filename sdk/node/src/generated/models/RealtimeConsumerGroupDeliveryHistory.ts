/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RealtimeConsumerGroupDeliveryHistoryEvent } from './RealtimeConsumerGroupDeliveryHistoryEvent.js';
/**
 * Realtime Consumer Group Delivery History.
 */
export type RealtimeConsumerGroupDeliveryHistory = {
  events: Array<RealtimeConsumerGroupDeliveryHistoryEvent>;
  has_more: boolean;
  next_after_id: string;
  group_pruned_events: string;
  retention_seconds: 86400;
};

