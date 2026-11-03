/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RealtimeConsumerGroupFilter } from './RealtimeConsumerGroupFilter.js';
/**
 * Realtime Consumer Group.
 */
export type RealtimeConsumerGroup = {
  /**
   * Immutable event ID metadata field; empty disables deduplication.
   */
  deduplication_metadata_key: string;
  /**
   * Retention from first assignment; zero when disabled.
   */
  deduplication_window_seconds: number;
  /**
   * Cumulative source events skipped as duplicate IDs. Separate from filtering and completions; no snapshots, lease attempts, claim-rate credit or fencing tokens are allocated.
   */
  duplicate_messages: string;
  /**
   * Currently unexpired remembered event ID hashes. Idle expired rows are pruned on the next keyed assignment.
   */
  deduplication_keys: number;
  group_id: string;
  name: string;
  channel: string;
  assigned_sequence: string;
  latest_sequence: string;
  oldest_sequence: string;
  unassigned: string;
  completed: string;
  ready: number;
  leased: number;
  expired_leases: number;
  dead: number;
  active_workers: number;
  history_unavailable: boolean;
  capacity_blocked: boolean;
  /**
   * Realtime trigger consuming this group. Absent when workers consume it directly.
   */
  trigger_id?: string;
  lease_seconds: number;
  max_attempts: number;
  created_at: string;
  /**
   * New claims are paused; existing leases remain valid.
   */
  paused: boolean;
  paused_at: string | null;
  draining: boolean;
  /**
   * Drain mode is active and no unexpired leases remain; not a handler completion receipt.
   */
  drained: boolean;
  /**
   * this realtime operation. Immutable exact valid metadata field. Empty disables ordering. Missing/empty values share one ordered bucket.
   */
  ordering_metadata_key: string;
  /**
   * this realtime operation. Immutable ordered-group policy. Block holds later events until replay/repair; skip lets later events proceed. Explicit replay can occur after later work completed.
   */
  dead_letter_policy: 'block' | 'skip';
  /**
   * Ready snapshots held by ordering predecessors or leases; ready counts still include them.
   */
  ordering_blocked: number;
  /**
   * Latest committed fencing number for this group UUID; starts at zero.
   */
  last_fencing_token: string;
  /**
   * Realtime consumer group properties dead letter retention seconds. Zero keeps indefinitely; otherwise 3600 to 2592000 seconds. Ordered groups require skip policy. Cleanup includes a 300-second warning grace.
   */
  dead_letter_retention_seconds: number;
  /**
   * Cumulative snapshots removed by retention; not successful completions.
   */
  expired_dead_letters: string;
  /**
   * this realtime operation. Immutable opt-in metadata field containing an RFC3339 delivery deadline. Ordered groups require skip policy. Missing or empty values have no deadline. Malformed values become terminal invalid_delivery_deadline failures. Frozen on assignment; existing snapshots are unchanged. Deadlines prevent expired claims and cap leases.
   */
  deadline_metadata_key: string;
  /**
   * Cumulative deadline-expired or malformed-deadline deliveries made terminal by claim/replay maintenance. Not successful completions.
   */
  expired_deliveries: string;
  filter: RealtimeConsumerGroupFilter;
  /**
   * Cumulative nonmatching retained events skipped during assignment; does not count completions or explicit retention-gap resynchronization.
   */
  filtered_messages: string;
};

