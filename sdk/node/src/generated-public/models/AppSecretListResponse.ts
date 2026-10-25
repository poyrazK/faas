/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppSecretResponse } from './AppSecretResponse.js';
import type { ScopedAppSecretResponse } from './ScopedAppSecretResponse.js';
/**
 * Paginated list of sealed-secret envelopes (no plaintext). Discriminated union: `secrets` is the flat arm; `secrets_by_scope` is the `?scope=__all__` arm (ADR-092 PR-B).
 */
export type AppSecretListResponse = {
  secrets: Array<AppSecretResponse>;
  /**
   * Nested per-scope map (ADR-092 PR-B, mirror of ADR-090 PR-B D3). Populated only when `?scope=__all__` is passed; keys are scope names, values are per-scope row lists ordered by key ASC.
   */
  secrets_by_scope?: Record<string, Array<ScopedAppSecretResponse>>;
  quota_max: number;
  count: number;
};

