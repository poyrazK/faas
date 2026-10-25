/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Provider-independent customer billing status (issue
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
};

