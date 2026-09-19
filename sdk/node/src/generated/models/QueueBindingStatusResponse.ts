/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Read-only queue binding consumer projection and queue counters.
 */
export type QueueBindingStatusResponse = {
  binding_id: string;
  name: string;
  queue_name: string;
  mode: 'pull' | 'push';
  workload_class: 'worker' | 'job';
  enabled: boolean;
  consumer_state: 'active' | 'paused' | 'not_configured' | 'external';
  consumer_state_reason?: string;
  consumer_liveness: 'healthy' | 'degraded' | 'stale' | 'not_observed' | 'external';
  trigger_id?: string;
  last_poll_at?: string | null;
  last_success_at?: string | null;
  last_error_at?: string | null;
  last_error?: string;
  lag_messages?: number | null;
  lag_age_seconds?: number | null;
  depth: number;
  in_flight: number;
  dead_letter: number;
  oldest_pending_at?: string | null;
  oldest_pending_age_seconds?: number | null;
  generated_at: string;
};

