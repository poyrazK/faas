/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Realtime Consumer Group Lag Alert Config.
 */
export type RealtimeConsumerGroupLagAlertConfig = {
  enabled: boolean;
  /**
   * 0 disables backlog threshold; maximum 1000000000.
   */
  unassigned_threshold?: string;
  oldest_pending_seconds?: number;
  dead_letter_threshold?: number;
  cooldown_seconds?: number;
};

