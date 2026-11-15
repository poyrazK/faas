/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A new account linked to an abuse-held account.
 */
export type AccountRiskLink = {
  account_id: string;
  held_account_id: string;
  held_org_id?: string;
  held_reason: string;
  signal: 'signup_network' | 'email_domain';
  detected_at: string;
};

