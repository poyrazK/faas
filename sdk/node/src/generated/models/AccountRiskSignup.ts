/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The account's sign-up record. signup_ip and user_agent only with include_pii, and only for 90 days.
 */
export type AccountRiskSignup = {
  method: string;
  signup_ip?: string;
  user_agent?: string;
  country?: string;
  asn?: number;
  asn_org?: string;
  email_domain: string;
  disposable_email: boolean;
  created_at: string;
  reduced_at?: string;
};

