/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventOrderingBlocker } from './EventOrderingBlocker.js';
/**
 * One application or workflow event recipient awaiting routing, without envelope data.
 */
export type EventBacklogRecipient = {
  event_source: string;
  event_id: string;
  event_type: string;
  accepted_at: string;
  app_id: string;
  /**
   * Empty when the captured app is no longer owned by this account.
   */
  app_slug: string;
  target_available: boolean;
  subscription_id: string;
  consumer_kind: 'application' | 'workflow';
  /**
   * Whether this recipient was captured at publication or added by historical backfill.
   */
  origin: 'acceptance' | 'backfill';
  /**
   * Captured workflow trigger name; present for workflow consumers.
   */
  workflow_name?: string;
  routing_mode: 'event' | 'recipient';
  state: 'pending' | 'processing';
  /**
   * Last recorded capacity scope for a currently pending recipient.
   */
  capacity_scope?: 'consumer' | 'app' | 'account';
  attempts: number;
  capacity_deferrals: number;
  next_attempt_at?: string;
  /**
   * Recipient lease or shared whole-receipt lease.
   */
  lease_until?: string;
  /**
   * Age from acceptance at observed_at, not time spent at the latest capacity scope.
   */
  pending_age_seconds: number;
  /**
   * Active routing and shared receipt leases take precedence; subscription controls precede ordering, recorded capacity and retry backoff.
   */
  waiting_reason: 'circuit_open' | 'circuit_probe_wait' | 'circuit_recovery_rate_limited' | 'subscription_paused' | 'subscription_rate_limited' | 'ordering_blocked' | 'capacity_consumer' | 'capacity_app' | 'capacity_account' | 'routing_in_progress' | 'receipt_processing' | 'retry_backoff' | 'workflow_routing' | 'ready';
  receipt_url: string;
  ordering_blocker?: EventOrderingBlocker;
  /**
   * Present for application subscriptions whose captured target still belongs to the account. Workflow admission is visible through the receipt. History coverage is independently bounded.
   */
  fanout_history_url?: string;
};

