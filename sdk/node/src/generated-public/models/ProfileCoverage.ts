/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfileAttributionReason } from './ProfileAttributionReason.js';
/**
 * Recorded collection evidence. Overlapping intervals count once; gaps can include idle time or loss. Failure counts are a lower bound; losses before ingestion are unknown. Unavailable coverage must not be interpreted as zero collection.
 */
export type ProfileCoverage = {
  /**
   * Optional host-generated whole-capture CPU diagnostics. Legacy captures have no counters; public attribution quality reconciles counters against merged CPU before declaring completeness.
   */
  attribution_reasons?: Array<ProfileAttributionReason>;
  available: boolean;
  received_profiles: number;
  contributing_collectors: number;
  /**
   * Latest recorded receipt among profiles in the selected capture window.
   */
  last_received_at?: string;
  window_seconds: number;
  covered_seconds: number;
  gap_seconds: number;
  /**
   * Rate limited recorded failures; a lower bound on failed attempts rather than exact lost profiles.
   */
  recorded_failed_uploads: number;
  /**
   * False because failures before ingestion and omitted failure records are unknown.
   */
  failures_complete: boolean;
};

