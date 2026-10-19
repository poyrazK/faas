/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsAlertCollectionRejection } from './ObsAlertCollectionRejection.js';
/**
 * One immutable failed-attempt receipt with optional sanitized evidence captured from its rejected source snapshot.
 */
export type ObsAlertCollectionFailure = {
  attempted_at: string;
  reason: 'collection_failed' | 'source_read_failed' | 'snapshot_rejected' | 'timeout' | 'persistence_failed';
  /**
   * Recorded only when typed historical rejection evidence is present; unknown never implies healthy rules.
   */
  diagnostics_state: 'recorded' | 'unknown';
  /**
   * Null for old receipts or failures without a rejected readable source; present only for snapshot_rejected.
   */
  diagnostics: (ObsAlertCollectionRejection | null);
};

