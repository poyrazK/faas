/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Per-row shape for the nested `secrets_by_scope` response
 * (ADR-092 PR-B, mirror of ADR-090 D3's env_by_scope).
 * Same posture as AppSecretResponse but with an explicit
 * `scope` field that carries the scope name on the wire
 * so a CLI / dashboard can render "scope: staging" without
 * a second lookup. Value is NEVER echoed (same posture as
 * AppSecretResponse).
 *
 */
export type ScopedAppSecretResponse = {
  scope: string;
  key: string;
  created_at: string;
  updated_at: string;
  /**
   * age-1... recipient string of the host identity that sealed this row (ADR-089). Empty for rows sealed before migration 00166. Mirrors the `kid` field on the parent `AppSecretResponse` — see that schema for the cross-reference.
   */
  kid?: string;
  /**
   * 16-hex HMAC-SHA256(plaintext) keyed by the per-host host.hmac.key (ADR-117 PR-C). Empty for pre-PR-C rows.
   */
  value_hash?: string;
  /**
   * Opaque monotonic version of the runtime value.
   */
  delivery_version: number;
  /**
   * Newest version confirmed in a successfully started runtime.
   */
  delivered_version?: number;
  delivery_status: 'pending' | 'delivered' | 'failed';
  last_delivery_attempt_at?: string;
  last_delivered_at?: string;
  last_delivery_error_code?: 'runtime_start_failed';
  last_delivered_wake_id?: string;
  last_delivered_instance_id?: string;
};

