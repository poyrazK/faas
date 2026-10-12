/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RealtimeConsumerGroupPriorityBacklog } from './RealtimeConsumerGroupPriorityBacklog.js';
/**
 * Realtime Consumer Group Priority Status.
 */
export type RealtimeConsumerGroupPriorityStatus = {
  metadata_key: string;
  fair_turns: 8;
  next_claim_oldest: boolean;
  backlog: Array<RealtimeConsumerGroupPriorityBacklog>;
};

