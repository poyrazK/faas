/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A sealed private-registry credential envelope: registry + username
 * + timestamps. Plaintext password NEVER appears in this shape.
 *
 */
export type AppRegistryCredentialResponse = {
  registry: string;
  username: string;
  created_at: string;
  updated_at: string;
  /**
   * Timestamp of the last successful authenticated pull. Omitted when the credential has not been used yet.
   */
  last_used_at?: string;
};

