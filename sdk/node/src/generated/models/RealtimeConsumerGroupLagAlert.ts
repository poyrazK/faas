/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Realtime Consumer Group Lag Alert.
 */
export type RealtimeConsumerGroupLagAlert = {
  enabled: boolean;
  /**
   * this realtime operation. 0 disables backlog threshold; maximum 1000000000.
   */
  unassigned_threshold: string;
  oldest_pending_seconds: number;
  dead_letter_threshold: number;
  cooldown_seconds: number;
  breached: boolean;
  unassigned: string | null;
  oldest_pending_age_seconds: number | null;
  dead_letters: number | null;
  observed_at: string | null;
  /**
   * Last event decision, including when no recipient opted in.
   */
  last_event_at: string | null;
};

