/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Per-app rollup for one calendar day (informational; not billed). Mirrors the `usage_daily` table populated by the meterd rollup loop (ADR-048 §5, migration 00067).
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
   * Cumulative host cgroup CPU-µs for the day (informational; not billed). ADR-046 / #279.
   */
  cpu_usec: number;
  /**
   * HTTP response bytes for the day (informational; not billed). ADR-046.
   */
  tx_bytes: number;
  /**
   * Cumulative net_tx_bytes for the day (informational; not billed). ADR-046.
   */
  net_tx_bytes: number;
  /**
   * Cumulative ingress bytes for the day (informational; not billed). ADR-048.
   */
  net_rx_bytes: number;
  /**
   * Per-day WAKE_RESTORE→WAKE_COLD_BOOT transition count (informational; not billed). ADR-048.
   */
  cold_boots: number;
  /**
   * Builder VM seconds burned by this app on this day (informational; not billed). ADR-048.
   */
  builder_seconds: number;
};

