/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedPostgresUsageImportReading } from './ManagedPostgresUsageImportReading.js';
/**
 * A complete policy-sized usage window with its actual source observation time.
 */
export type ManagedPostgresUsageImportWindow = {
  from: string;
  to: string;
  /**
   * Actual export observation time, at or after the closed window and no later than the server clock; microsecond precision maximum. Import time is never substituted.
   */
  observed_at: string;
  readings: Array<ManagedPostgresUsageImportReading>;
};

