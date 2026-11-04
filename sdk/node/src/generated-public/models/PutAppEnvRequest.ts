/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Set an env var: plaintext value (persisted verbatim in app_envs, non-sensitive by contract).
 */
export type PutAppEnvRequest = {
  /**
   * Plaintext env-var value. Stored as-is; do NOT use this surface for credentials.
   */
  value: string;
};

