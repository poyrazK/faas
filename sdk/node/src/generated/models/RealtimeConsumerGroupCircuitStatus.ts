/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Realtime Consumer Group Circuit Status.
 */
export type RealtimeConsumerGroupCircuitStatus = {
  enabled: boolean;
  failure_threshold: number;
  cooldown_seconds: number;
  state: 'closed' | 'open' | 'half_open';
  reason: string;
  consecutive_failures: number;
  next_probe_at: string | null;
  probe_sequence: string | null;
  /**
   * Breaker admission gate only; manual pause and other eligibility checks are separate.
   */
  blocked: boolean;
  retry_after_milliseconds: number;
};

