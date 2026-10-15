/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventAcknowledgmentRecordingCoverage } from './EventAcknowledgmentRecordingCoverage.js';
/**
 * Recorded renewal and expiry operations in full UTC minute buckets with optional verification of deployment-declared writer continuity.
 */
export type EventAcknowledgmentWindowHealth = {
  /**
   * Inclusive boundary of full minutes wholly inside the requested processing window.
   */
  window_start: string;
  recording_coverage?: EventAcknowledgmentRecordingCoverage;
  /**
   * Exclusive completed-minute boundary capped by the verified writer heartbeat watermark when available.
   */
  window_end: string;
  /**
   * Fixed width of each durable operation bucket.
   */
  bucket_seconds: number;
  /**
   * Metric retention measured from bucket start independently of invocation pruning.
   */
  retention_seconds: number;
  /**
   * Only instrumented writers contribute; inspect recording_coverage for declared-inventory continuity and no pre-upgrade activity is reconstructed.
   */
  coverage: 'recorded_full_minute_buckets';
  /**
   * Explicit successful renewals recorded in selected complete buckets.
   */
  renewal_successes: number;
  /**
   * Current-token renewal conflicts recorded after token and attempt verification.
   */
  renewal_rejections: number;
  /**
   * Sum of recorded successful and rejected renewals used as the fraction denominator.
   */
  renewal_attempts: number;
  /**
   * Recorded deferred-awaiting scheduler expiry transitions in these minutes.
   */
  lease_expiries: number;
  /**
   * At least one bucket overflowed its saturating counter; totals are lower bounds and rate fields are omitted.
   */
  saturated: boolean;
  /**
   * Recorded rejected renewals divided by recorded renewal attempts; absent without samples or complete minutes or after saturation.
   */
  renewal_rejection_fraction?: number;
  /**
   * Recorded lease expiry transitions divided by the effective window duration; absent for an empty or saturated window.
   */
  lease_expiry_rate_per_second?: number;
};

