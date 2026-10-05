/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedPostgresCutoverMember } from './ManagedPostgresCutoverMember.js';
/**
 * Staged cutover metadata. Verified means control-plane SQL evidence; workloads still use the source. Provider identities and sealed credentials are never returned.
 */
export type ManagedPostgresCutover = {
  id: string;
  source_database_id: string;
  target_database_id: string;
  app_id: string;
  scope: string;
  state: 'preparing' | 'prepared' | 'verifying' | 'verified' | 'cancelling' | 'cancelled';
  /**
   * All staged members have SQL evidence no older than verification_max_age_seconds.
   */
  verification_fresh: boolean;
  verification_max_age_seconds: number;
  verified_at?: string;
  last_error_code?: string;
  members: Array<ManagedPostgresCutoverMember>;
  created_at: string;
  updated_at: string;
};

