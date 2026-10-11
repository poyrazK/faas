/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventCircuitBreakerResponse } from './EventCircuitBreakerResponse.js';
/**
 * Current subscription backlog and bounded recorded routing outcomes. History compaction can remove outcomes; rates then represent retained samples. Latency runs from event acceptance to successful routing admission.
 */
export type EventConsumerHealth = {
  /**
   * Recorded delivery_expired outcomes in the observation window; included in terminal_failures.
   */
  expired_deliveries?: number;
  circuit_breaker?: EventCircuitBreakerResponse;
  subscription_id: string;
  app_id: string;
  paused: boolean;
  rate_per_second: number;
  pending_recipients: number;
  processing_recipients: number;
  oldest_pending_at?: string;
  oldest_age_seconds: number;
  /**
   * Absent until an operator sets a control.
   */
  updated_at?: string;
  observed_at: string;
  window_start: string;
  coverage: 'bounded_recorded_outcomes';
  paused_seconds: number;
  retry_scheduled: number;
  successful_routes: number;
  terminal_failures: number;
  retry_rate_per_second: number;
  terminal_failure_pct: number;
  routing_latency_p95_seconds: number;
  drain_rate_per_second: number;
  history_compacted: boolean;
};

