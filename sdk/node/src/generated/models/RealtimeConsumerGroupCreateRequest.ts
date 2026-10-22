/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RealtimeConsumerGroupFilter } from './RealtimeConsumerGroupFilter.js';
/**
 * Realtime Consumer Group Create Request.
 */
export type RealtimeConsumerGroupCreateRequest = {
  /**
   * Immutable opt-in exact metadata field such as event_id. Empty disables assignment-time deduplication. Missing or empty values process normally. Within the window, the first filter-matching assigned event ID wins even if its snapshot later fails or expires. Retries and explicit dead-letter replay reuse the original snapshot. Store hashes only, at most 4096 live IDs per group; live IDs are never evicted. New IDs wait for capacity while existing snapshots remain claimable.
   */
  deduplication_metadata_key?: string;
  /**
   * Immutable retention from first assignment. With a metadata key, omit or use zero for the 3600-second default; otherwise choose 60 to 86400. Without a key, must be zero. Duplicate hits do not extend expiry. After expiry, another copy can be assigned again.
   */
  deduplication_window_seconds?: number;
  name: string;
  after_sequence: string;
  lease_seconds?: number;
  max_attempts?: number;
  /**
   * Immutable exact valid metadata field. Empty disables ordering. Missing/empty values share one ordered bucket.
   */
  ordering_metadata_key?: string;
  /**
   * Immutable ordered-group policy. Block holds later events until replay/repair; skip lets later events proceed. Explicit replay can occur after later work completed.
   */
  dead_letter_policy?: 'block' | 'skip';
  /**
   * Zero keeps indefinitely; otherwise 3600 to 2592000 seconds. Ordered groups require skip policy. Cleanup includes a 300-second warning grace.
   */
  dead_letter_retention_seconds?: number;
  /**
   * Immutable opt-in metadata field. Exact single digits 0 through 9 freeze on assignment; missing or invalid values use zero. Higher values claim first within the bounded 128-snapshot window. Every eighth successful claim uses oldest eligible sequence; entity ordering remains authoritative. Empty disables priority.
   */
  priority_metadata_key?: string;
  /**
   * Immutable opt-in metadata field containing an RFC3339 delivery deadline. Ordered groups require skip policy. Missing or empty values have no deadline. Malformed values become terminal invalid_delivery_deadline failures. Frozen on assignment; existing snapshots are unchanged. Deadlines prevent expired claims and cap leases.
   */
  deadline_metadata_key?: string;
  filter?: RealtimeConsumerGroupFilter;
};

