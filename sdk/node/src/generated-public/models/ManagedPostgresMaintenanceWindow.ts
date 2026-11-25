/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Recurring UTC time range when database maintenance can begin.
 */
export type ManagedPostgresMaintenanceWindow = {
  /**
   * UTC weekday (Sunday = 0, Saturday = 6).
   */
  weekday: number;
  /**
   * Minutes after midnight UTC.
   */
  start_minute: number;
  /**
   * Length of the maintenance window.
   */
  duration_minutes: number;
};

