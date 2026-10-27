/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Per-app rollup for one calendar day (informational; not billed). Mirrors the `usage_daily` table populated by the metering service rollup loop.
 */
export type DailyUsageResponse = {
  app_id: string;
  day: string;
  /**
   * Cumulative mb_seconds for the day (informational; not billed).
   */
  mb_seconds: number;
  /**
   * Cumulative request count for the day (informational; not billed).
   */
  requests: number;
  /**
   * Cumulative host cgroup CPU-µs for the day (informational; not billed).
   */
  cpu_usec: number;
  /**
   * HTTP response bytes for the day (informational; not billed).
   */
  tx_bytes: number;
  /**
   * Cumulative net_tx_bytes for the day (informational; not billed).
   */
  net_tx_bytes: number;
  /**
   * Cumulative ingress bytes for the day (informational; not billed).
   */
  net_rx_bytes: number;
  /**
   * Per-day WAKE_RESTORE→WAKE_COLD_BOOT transition count (informational; not billed).
   */
  cold_boots: number;
  /**
   * Builder VM seconds burned by this app on this day (informational; not billed).
   */
  builder_seconds: number;
};

