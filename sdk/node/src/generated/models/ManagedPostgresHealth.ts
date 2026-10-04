/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Cached read-only provider observation, separate from lifecycle state. Healthy does not prove SQL connectivity. Failed checks update checked_at while retaining last_success_at; stale observations must not imply current availability.
 */
export type ManagedPostgresHealth = {
  enabled: boolean;
  status: 'disabled' | 'unknown' | 'healthy' | 'degraded' | 'stale';
  fresh: boolean;
  provider_status: 'unknown' | 'missing' | 'pending' | 'ready' | 'deleting' | 'failed';
  compute_state: 'unknown' | 'active' | 'suspended' | 'waking';
  stale_after_seconds: number;
  /**
   * Latest metadata attempt, including provider request failures.
   */
  checked_at?: string;
  /**
   * Latest valid metadata response, including responses describing degraded resources.
   */
  last_success_at?: string;
  last_error_code?: 'resource_missing' | 'observer_unsupported' | 'provider_unavailable' | 'backend_unavailable' | 'observation_invalid' | 'spec_mismatch' | 'provider_failed';
};

