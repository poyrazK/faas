/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppEnvResponse } from './AppEnvResponse.js';
import type { ScopedAppEnvResponse } from './ScopedAppEnvResponse.js';
/**
 * List of env var envelopes (no plaintext values). Discriminated
 * union (ADR-090 PR-B D3):
 *
 * * `env` array populated, `env_by_scope` omitted: the
 * default-scope or per-scope read (omitted or `?scope=<slug>`).
 * `count` is the per-scope row count.
 * * `env` empty, `env_by_scope` populated: the `?scope=__all__`
 * read. `count` is the cross-scope row total.
 *
 * Both arms are valid wire shapes for a GET; the `?scope=`
 * query discriminates.
 *
 */
export type AppEnvListResponse = {
  env: Array<AppEnvResponse>;
  /**
   * Nested per-scope map (ADR-090 PR-B D3). Populated only when `?scope=__all__` is passed; keys are scope names, values are per-scope row lists ordered by key ASC.
   */
  env_by_scope?: Record<string, Array<ScopedAppEnvResponse>>;
  quota_max: number;
  count: number;
};

