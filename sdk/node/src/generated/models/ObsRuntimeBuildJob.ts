/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Independent selected-job inventory and source availability.
 */
export type ObsRuntimeBuildJob = {
  job: string;
  daemon: string;
  inventory_source: 'scrape_targets_minimum' | 'active_configured_compute_registry';
  state: 'available' | 'unknown';
  reason?: string;
  expected: number;
};

