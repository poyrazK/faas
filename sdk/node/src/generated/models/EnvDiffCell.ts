/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One (scope, row) cell in the env-diff matrix. The shape is
 * closed and uniform across row kinds; the difference is which
 * optional fields are populated. Security: secret cells never
 * emit a `value` field; env cells never emit a `value_hash`
 * field. Pre-PR-C rows have value_hash = '' and emit no field.
 *
 */
export type EnvDiffCell = {
  /**
   * True if the (row.key, scope) pair is stamped on the app; false if missing.
   */
  present: boolean;
  /**
   * 16-hex HMAC-SHA256(plaintext) keyed by the per-host host.hmac.key. Secret cells only.
   */
  value_hash?: string;
  /**
   * Plaintext env var. Env cells only; NEVER populated on secret cells (the load-bearing security property of the endpoint).
   */
  value?: string;
};

