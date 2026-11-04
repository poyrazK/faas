/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Create a deploy token scoped to one app. The server always assigns deploy:write.
 */
export type CreateDeployTokenRequest = {
  label?: string;
  /**
   * Optional future expiry; defaults to 90 days and is capped at 365 days.
   */
  expires_at?: string;
};

