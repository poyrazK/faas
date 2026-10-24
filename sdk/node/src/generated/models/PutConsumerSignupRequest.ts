/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Self-service sign-up settings (ADR-20261021143000). Omitted fields take their defaults.
 */
export type PutConsumerSignupRequest = {
  /**
   * Whether customers can start sign-ups (default true).
   */
  enabled?: boolean;
  /**
   * Where cards are saved (default stripe).
   */
  provider?: 'stripe';
  /**
   * Consumer plans customers choose from; none offers the app's default plan.
   */
  plan_ids?: Array<string>;
  /**
   * Scopes of the key a customer gets (default write and billing).
   */
  key_scopes?: Array<'read' | 'write' | 'admin' | 'billing'>;
  display_name: string;
  /**
   * https URL customers accept before signing up.
   */
  terms_url?: string;
  /**
   * https URL shown after the key; it never carries the key.
   */
  return_url?: string;
  trial_days?: number;
  /**
   * Sign-ups per UTC day (default 100).
   */
  daily_limit?: number;
  /**
   * Let customers switch between plan_ids with their own key (ADR-20261021183000).
   */
  customer_plan_changes?: boolean;
  /**
   * Let customers close their account with their own key (ADR-20261021183000).
   */
  customer_closing?: boolean;
};

