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
  /**
   * Opaque monotonic version of the runtime value. Advances on value mutation, not host-key reseal.
   */
  delivery_version: number;
  /**
   * Newest version confirmed in a successfully started runtime. Omitted until the first successful delivery.
   */
  delivered_version?: number;
  /**
   * Delivery state for the current delivery_version. A concurrent rotation remains pending until that exact version starts successfully.
   */
  delivery_status: 'pending' | 'delivered' | 'failed';
  last_delivery_attempt_at?: string;
  last_delivered_at?: string;
  /**
   * Non-sensitive closed failure reason for the current version; omitted unless delivery_status is failed.
   */
  last_delivery_error_code?: 'runtime_start_failed';
  /**
   * Wake correlation id of the most recent successful delivery.
   */
  last_delivered_wake_id?: string;
  /**
   * Runtime instance that most recently received a secret version.
   */
  last_delivered_instance_id?: string;
};

