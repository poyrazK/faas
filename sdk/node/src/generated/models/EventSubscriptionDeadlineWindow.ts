/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventSubscriptionPendingSummary } from './EventSubscriptionPendingSummary.js';
/**
 * Complete, disjoint deadline cohorts with the full pending summary from one read-only snapshot. Upcoming means observed_at < deadline < window_end_at; expired means deadline <= observed_at. Incomplete summary coverage invalidates all counts for alert confirmation.
 */
export type EventSubscriptionDeadlineWindow = {
  coverage: 'all_retained_captured_pending_application_deadline_window';
  summary: EventSubscriptionPendingSummary;
  within_millis: number;
  window_end_at: string;
  upcoming_recipients: number;
  /**
   * Omitted exactly when upcoming_recipients is zero.
   */
  earliest_upcoming_deadline_at?: string;
  /**
   * Exact partition of upcoming_recipients over every known backlog waiting reason, including explicit zero keys. Labels do not establish unique causes.
   */
  upcoming_waiting_reasons: Record<string, number>;
  /**
   * Exact partition of summary.already_expired_count over every known backlog waiting reason, including explicit zero keys.
   */
  expired_waiting_reasons: Record<string, number>;
};

