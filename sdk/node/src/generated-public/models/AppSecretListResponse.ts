/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppSecretResponse } from './AppSecretResponse.js';
import type { ScopedAppSecretResponse } from './ScopedAppSecretResponse.js';
/**
 * Paginated list of sealed-secret envelopes (no plaintext). Discriminated union: `secrets` is the flat arm; `secrets_by_scope` is the `?scope=__all__` arm.
 */
export type AppSecretListResponse = {
  secrets: Array<AppSecretResponse>;
  /**
   * Nested per-scope map of secrets. Populated only when `?scope=__all__` is passed; keys are scope names, values are per-scope row lists ordered by key ASC.
   */
  secrets_by_scope?: Record<string, Array<ScopedAppSecretResponse>>;
  quota_max: number;
  count: number;
};

