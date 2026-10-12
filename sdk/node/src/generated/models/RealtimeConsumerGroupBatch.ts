/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RealtimeConsumerGroupClaim } from './RealtimeConsumerGroupClaim.js';
/**
 * Realtime Consumer Group Batch.
 */
export type RealtimeConsumerGroupBatch = {
  /**
   * A new event ID encountered the 4096-key bound; its source cursor is unchanged. Partial claims may be present. Existing snapshots bypass deduplication and remain claimable. Wait using retry_after_milliseconds before assigning new IDs.
   */
  deduplication_blocked: boolean;
  /**
   * Circuit cooldown or an active probe stopped new claims; partial claims may be present.
   */
  circuit_open: boolean;
  claims: Array<RealtimeConsumerGroupClaim>;
  history_unavailable: boolean;
  capacity_blocked: boolean;
  /**
   * this realtime operation. New claims are paused; existing leases remain valid.
   */
  paused: boolean;
  /**
   * Ready snapshots are blocked by same-key predecessors or leases. This is normal backpressure, not a fatal error.
   */
  ordering_blocked: boolean;
  /**
   * Shared group rate stopped admission, possibly after partial claims.
   */
  rate_throttled: boolean;
  /**
   * Minimum observed rate or circuit delay or time until an event ID expires. Positive when rate_throttled, circuit_open or deduplication_blocked.
   */
  retry_after_milliseconds: number;
  /**
   * Assignment reached the 128-event scan budget. Progress is committed; partial claims may be present. Poll again to continue; more work is not guaranteed.
   */
  filter_scan_limited: boolean;
};

