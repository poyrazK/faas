/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Per-app storage rollup for one calendar day (informational; not billed today). Mirrors the `snapshot_storage_daily` table populated by the metering service storage rollup loop.
 */
export type StorageUsageResponse = {
  app_id: string;
  day: string;
  /**
   * Cumulative snapshot bytes (mem_bytes + disk_bytes) for the day (informational; not billed today).
   */
  snapshot_bytes: number;
  /**
   * Cumulative overlay-staging layer bytes for the day (informational; not billed today).
   */
  layer_bytes: number;
};

