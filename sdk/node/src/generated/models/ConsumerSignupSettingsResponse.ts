/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * An app's sign-up settings.
 */
export type ConsumerSignupSettingsResponse = {
  app: string;
  enabled: boolean;
  provider: 'stripe';
  plan_ids: Array<string>;
  key_scopes: Array<string>;
  display_name: string;
  terms_url?: string;
  return_url?: string;
  trial_days?: number;
  daily_limit: number;
  /**
   * The hosted sign-up page to link customers to.
   */
  page_url: string;
  customer_plan_changes: boolean;
  customer_closing: boolean;
  created_at: string;
  updated_at: string;
};

