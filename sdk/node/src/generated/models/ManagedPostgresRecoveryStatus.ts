/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Live metadata recovery observation. limits_known establishes necessary limits only; available additionally requires authoritative provider history bounds. Neither is an atomic guarantee of a later restore. Private provider identifiers and credentials are excluded.
 */
export type ManagedPostgresRecoveryStatus = {
  database_id: string;
  status: 'limits_known' | 'available' | 'unavailable' | 'unknown' | 'unsupported';
  /**
   * True only when current source metadata was successfully validated.
   */
  fresh: boolean;
  /**
   * True only when the provider supplies authoritative retained history bounds. Neon currently supplies necessary limits only.
   */
  history_bounds_known: boolean;
  /**
   * Effective observed retention intersected with catalog limits; zero when unconfirmed or disabled.
   */
  retention_seconds: number;
  /**
   * Completion time of the latest provider observation attempt.
   */
  checked_at?: string;
  /**
   * Necessary lower limit; does not by itself prove retained WAL begins here.
   */
  earliest_possible_time?: string;
  /**
   * Necessary upper limit; new restore points must also precede checked_at.
   */
  latest_possible_time?: string;
  /**
   * Stable non-sensitive diagnostic; no provider response text.
   */
  last_error_code?: string;
};

