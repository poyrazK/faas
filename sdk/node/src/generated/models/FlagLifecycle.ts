/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One flag's owner, expiry, last change and recent use.
 */
export type FlagLifecycle = {
  key: string;
  owner?: string;
  expires_at?: string;
  /**
   * Newest version that changed this flag, within the last 200 versions.
   */
  last_changed_version: number;
  last_changed_at: string;
  /**
   * True when every request gets the same value (disabled, killed, no rules, or a first rule matching everybody).
   */
  serves_one_value: boolean;
  /**
   * Requests with a decision for this flag in the last 7 days of retained telemetry.
   */
  evaluated_requests?: number;
  evidence: 'available' | 'unavailable';
  attention: Array<'expired' | 'expires_soon' | 'stale' | 'never_evaluated' | 'no_owner'>;
};

