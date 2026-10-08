/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventCircuitBreakerPolicy } from './EventCircuitBreakerPolicy.js';
export type EventCircuitBreakerResponse = {
  subscription_id: string;
  enabled: boolean;
  policy?: EventCircuitBreakerPolicy;
  state: 'disabled' | 'closed' | 'open' | 'half_open' | 'draining';
  /**
   * Reason for the last durable transition.
   */
  reason?: string;
  changed_at?: string;
  cooldown_until?: string;
  probe_in_flight: boolean;
  successful_probes: number;
  recovery_rate_per_second: number;
  /**
   * Compacted history prevents automatic threshold decisions or completion of recovery.
   */
  history_incomplete: boolean;
  manual_paused: boolean;
};

