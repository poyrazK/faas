/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Global count and oldest durable phase clock, independent of detail pagination.
 */
export type DataRepairPhaseHealth = {
  phase: 'queued' | 'running' | 'cleanup' | 'publishing' | 'applying' | 'outcome_unknown' | 'replacing' | 'resolving';
  count: number;
  oldest_phase_started_at?: string;
  oldest_age_seconds: number;
  /**
   * Unknown apply outcomes require attention immediately.
   */
  warn_after_seconds: number;
};

