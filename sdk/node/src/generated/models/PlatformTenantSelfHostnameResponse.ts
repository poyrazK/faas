/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A redacted hostname intent and DNS TXT challenge. DNS and certificate status remain asynchronous.
 */
export type PlatformTenantSelfHostnameResponse = {
  surface_id: string;
  hostname: string;
  action: 'created' | 'unchanged';
  verified: boolean;
  verified_at?: string;
  /**
   * DNS TXT record name to publish for ownership proof.
   */
  txt_record: string;
  /**
   * Returned only while DNS ownership remains unverified; store securely and publish as the TXT value.
   */
  challenge_token?: string;
};

