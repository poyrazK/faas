/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { SecretRuntimeReloadObservation } from './SecretRuntimeReloadObservation.js';
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
  /**
   * Secret version associated with the latest guest-init projection/signal observation; compare with delivery_version to detect stale status. Not an application acknowledgement.
   */
  last_runtime_reload_version?: number;
  last_runtime_reload_projection?: 'updated' | 'unchanged' | 'failed';
  /**
   * Guest-init sent/queued signal outcome; does not mean the application applied the new credentials.
   */
  last_runtime_reload_signal?: 'sent' | 'queued' | 'failed' | 'not_attempted';
  last_runtime_reload_at?: string;
  last_runtime_reload_error_code?: 'projection_failed' | 'signal_failed';
  last_runtime_reload_instance_id?: string;
  /**
   * Latest guest-init outcome from each active reporting runtime. Missing reports are unknown, and successful signals do not prove application acknowledgement.
   */
  runtime_reload_observations?: Array<SecretRuntimeReloadObservation>;
};

