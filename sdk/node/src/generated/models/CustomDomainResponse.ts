/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A custom domain binding: domain string, target app, verification status, and TLS provisioning state.
 */
export type CustomDomainResponse = {
  domain: string;
  app_id: string;
  challenge_token?: string | null;
  verified: boolean;
  verified_at?: string | null;
  txt_record?: string | null;
};

