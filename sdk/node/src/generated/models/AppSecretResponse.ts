/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A sealed secret envelope: key name, sealed ciphertext (server can't read it), version, and timestamps. Scope is the env-scope the row belongs to (ADR-092 PR-B). Pre-PR-B callers see scope='default' echoed on every row.
 */
export type AppSecretResponse = {
  key: string;
  scope: string;
  created_at: string;
  updated_at: string;
  /**
   * age-1... recipient string of the host identity that sealed this row (ADR-089). Empty for rows sealed before migration 00166.
   */
  kid?: string;
  /**
   * 16-hex HMAC-SHA256(plaintext) keyed by the per-host host.hmac.key (ADR-117 PR-C). Empty for pre-PR-C rows. Same value across scopes = byte-identical plaintext.
   */
  value_hash?: string;
};

