/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Local evidence for one accountable database. Shared restores refer to their accounting root. Reasons explain stale admission only; an empty list does not establish invoice settlement or remaining budget. An unknown legacy tombstone has no confirmed terminal deadline.
 */
export type ManagedPostgresAccountingDiagnostic = {
  database_id: string;
  name: string;
  state: 'provisioning' | 'ready' | 'updating' | 'deleting' | 'failed' | 'deleted';
  accounting_required: boolean;
  identity_known: boolean;
  accounting_database_id: string;
  /**
   * This resource blocks admission due to stale accounting under the enabled policy. False when the policy is disabled.
   */
  blocking: boolean;
  reasons: Array<'identity_unknown' | 'legacy_identity_unknown' | 'coverage_missing' | 'window_mismatch' | 'shutdown_unconfirmed' | 'coverage_incomplete' | 'observation_stale' | 'final_correction_pending'>;
  collected_window_seconds: number;
  required_from?: string | null;
  required_until?: string | null;
  collected_from?: string | null;
  collected_until?: string | null;
  observed_at?: string | null;
  correction_observed_at?: string | null;
  correction_required_at?: string | null;
  lease_until?: string | null;
};

