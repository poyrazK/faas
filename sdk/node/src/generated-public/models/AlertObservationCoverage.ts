/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AlertObservationSkipCount } from './AlertObservationSkipCount.js';
/**
 * Recorded live Observe only checks during the 24 hours before this response.
 */
export type AlertObservationCoverage = {
  /**
   * Recorded checks including skipped checks.
   */
  attempted: number;
  /**
   * Checks that evaluated the condition.
   */
  completed: number;
  /**
   * Recorded skips grouped by reason.
   */
  skipped?: Array<AlertObservationSkipCount>;
  /**
   * Most recent recorded check for this observation even if older than 24 hours.
   */
  last_checked_at?: string;
};

