/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventSubscriptionRateDeadlineRisk } from './EventSubscriptionRateDeadlineRisk.js';
import type { EventSubscriptionRateLimit } from './EventSubscriptionRateLimit.js';
/**
 * One snapshot of current controls and a hypothetical rate-only admission model. The deterministic oldest-first model is not an ordering guarantee, completion ETA or deadline-safety certificate. Full pending coverage excludes processing, unattributed receipts and future arrivals.
 */
export type EventSubscriptionRatePreview = {
  coverage: 'retained_captured_pending_application_recipients';
  app_id: string;
  subscription_id: string;
  observed_at: string;
  advisory: boolean;
  read_only: boolean;
  /**
   * Reviewed bucket revision for the subsequent mutation; other controls and backlog can change independently.
   */
  expected_revision: number;
  current: EventSubscriptionRateLimit;
  /**
   * Hypothetical capacity immediately after applying the proposal. Revision remains the reviewed baseline because no mutation occurred.
   */
  projected: EventSubscriptionRateLimit;
  paused: boolean;
  legacy_rate_per_second: number;
  circuit_state: string;
  capacity_blocking_scopes: Array<'consumer' | 'app' | 'account'>;
  /**
   * Current pause, circuit, capacity, lease, scheduled-retry, clock and sampling constraints. An empty list does not promise future readiness.
   */
  constraints: Array<string>;
  sample_limit: 1000;
  sampled_pending: number;
  /**
   * Captured pending sample is untruncated with usable timestamps; not evidence of eventual delivery.
   */
  complete: boolean;
  truncated: boolean;
  /**
   * Modeled admission span for the sample, assuming all unexpired sampled recipients are admitted once, no new arrivals, oldest-first order and no blocking from pause/circuit/capacity/leases or execution. Includes scheduled times and legacy pacing. Missing when clocks or duration are unusable. Truncated samples never estimate the full backlog.
   */
  rate_only_drain_millis?: number;
  deadline_risk_count: number;
  already_expired_count: number;
  deadline_risks: Array<EventSubscriptionRateDeadlineRisk>;
  risks_truncated: boolean;
};

