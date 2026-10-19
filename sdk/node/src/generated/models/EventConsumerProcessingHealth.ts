/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventAcknowledgmentWindowHealth } from './EventAcknowledgmentWindowHealth.js';
/**
 * Current retained deferred invocation gauges, retained completion latency and optional durable full-minute renewal metrics with independent coverage.
 */
export type EventConsumerProcessingHealth = {
  /**
   * Application owning this processing summary.
   */
  app_id: string;
  /**
   * Consumer subscription selected for the retained processing aggregate.
   */
  subscription_id: string;
  /**
   * Database snapshot clock for processing gauges.
   */
  observed_at: string;
  /**
   * Start of the retained acknowledgment completion window.
   */
  window_start: string;
  /**
   * Coverage is limited to currently retained opted-in invocations.
   */
  coverage: 'retained_deferred_invocations';
  /**
   * All matching retained invocations were aggregated without sampling; this is not historical completeness.
   */
  complete: boolean;
  /**
   * Deferred invocations currently pending dispatch.
   */
  waiting: number;
  /**
   * Live deferred dispatches not yet handed off.
   */
  delivering: number;
  /**
   * Unexpired matching leases handed off after HTTP 202.
   */
  processing: number;
  /**
   * Expired dispatching leases before recovery sweeps.
   */
  expired: number;
  /**
   * Retained opted-in invocations completed by callback or ordinary success.
   */
  completed: number;
  /**
   * Retained deferred invocations in dead letters.
   */
  dead_lettered: number;
  /**
   * Retained deferred invocations permanently failed.
   */
  failed: number;
  /**
   * Other terminal states including cancellation and supersession and work-policy expiry.
   */
  other: number;
  /**
   * Active handed-off leases in their final third; the risk horizon is capped by near_expiry_seconds.
   */
  near_expiry: number;
  /**
   * Maximum lease risk horizon in seconds; each claim uses the smaller of this cap and one third of its lease duration.
   */
  near_expiry_seconds: number;
  /**
   * Oldest token issue time among live handed-off claims; includes initial dispatch time.
   */
  oldest_processing_offered_at?: string;
  /**
   * Age since oldest active processing token issue; zero when absent.
   */
  oldest_processing_age_seconds: number;
  /**
   * Retained matching explicit completions whose completion time falls inside the window.
   */
  acknowledgment_samples: number;
  /**
   * Maximum token-issue-to-completion seconds among acknowledgment samples; zero requires inspecting sample count.
   */
  acknowledgment_latency_max_seconds: number;
  /**
   * Legacy non-windowed coverage marker retained for older clients; use renewal_window for durable recorded operation counts.
   * @deprecated
   */
  renewal_failure_coverage: 'not_recorded';
  renewal_window?: EventAcknowledgmentWindowHealth;
};

