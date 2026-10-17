/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventSubscriptionRateLimit } from './EventSubscriptionRateLimit.js';
/**
 * Full retained captured pending application-recipient counts from one read-only snapshot. No payloads, identities or modeled completion estimates. Incomplete evidence cannot establish health or recovery.
 */
export type EventSubscriptionPendingSummary = {
  coverage: 'all_retained_captured_pending_application_recipients';
  app_id: string;
  subscription_id: string;
  observed_at: string;
  read_only: boolean;
  /**
   * False for unusable clocks; incomplete counts must not drive alert recovery.
   */
  complete: boolean;
  pending_recipients: number;
  deadline_recipients: number;
  already_expired_count: number;
  /**
   * Earliest effective deadline, including expired rows; omitted when no pending recipients have deadlines.
   */
  earliest_deadline_at?: string;
  current: EventSubscriptionRateLimit;
  paused: boolean;
  legacy_rate_per_second: number;
  circuit_state: 'closed' | 'open' | 'half_open' | 'draining';
  capacity_blocking_scopes: Array<'consumer' | 'app' | 'account'>;
  constraints: Array<'subscription_paused' | 'legacy_rate_limit' | 'circuit_open' | 'circuit_half_open' | 'circuit_draining' | 'capacity_consumer' | 'capacity_app' | 'capacity_account' | 'active_leases' | 'scheduled_retries' | 'clock_uncertain'>;
};

