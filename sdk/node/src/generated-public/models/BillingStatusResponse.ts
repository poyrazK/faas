/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Provider-independent customer billing status.
 */
export type BillingStatusResponse = {
  mode: 'live' | 'disabled';
  enabled: boolean;
  /**
   * Configured provider name; no provider-specific identifiers are exposed.
   */
  provider: string;
  plan: 'free' | 'hobby' | 'pro' | 'scale';
  account_status: 'active' | 'past_due' | 'suspended' | 'deleted_pending';
  customer_configured: boolean;
  subscription_configured: boolean;
  usage_reconciliation_enabled: boolean;
  /**
   * When a scheduled cancellation ends the subscription instead of renewing it; the plan then returns to Free. Absent when none is scheduled.
   */
  subscription_ends_at?: string;
  /**
   * The plan a scheduled downgrade moves to at scheduled_plan_at. Absent when none is scheduled.
   */
  scheduled_plan?: 'free' | 'hobby' | 'pro' | 'scale';
  /**
   * When the scheduled downgrade applies.
   */
  scheduled_plan_at?: string;
};

