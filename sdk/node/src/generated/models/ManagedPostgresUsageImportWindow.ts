/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedPostgresUsageImportReading } from './ManagedPostgresUsageImportReading.js';
export type ManagedPostgresUsageImportWindow = {
  from: string;
  to: string;
  /**
   * Actual export observation time, at or after the closed window and no later than the server clock; microsecond precision maximum. Import time is never substituted.
   */
  observed_at: string;
  readings: Array<ManagedPostgresUsageImportReading>;
};

