/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsAlertCollectionFailure } from './ObsAlertCollectionFailure.js';
/**
 * Failed attempts within the inclusive requested window, independent of cadence carry-in and coalesced intervals.
 */
export type ObsAlertCollectionFailureDetails = {
  /**
   * Newest failed attempts first; diagnostics may be unknown on any item.
   */
  items: Array<ObsAlertCollectionFailure>;
  /**
   * Complete failed-attempt count matching coverage.failures before preview truncation.
   */
  total_failures: number;
  limit: number;
  /**
   * Whether older failed attempts are omitted from the details preview.
   */
  truncated: boolean;
  limitations: Array<string>;
};

