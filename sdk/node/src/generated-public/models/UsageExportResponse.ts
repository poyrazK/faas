/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One usage record: app id, GB-hours consumed, started/finished timestamps for the export window. cpu_usec is the per-(app, month) cumulative host cgroup CPU-µs. Informational only — billing is on mb_seconds.
 */
export type UsageExportResponse = {
  app_id: string;
  month: string;
  mb_seconds: number;
  requests: number;
  /**
   * Cumulative host cgroup CPU-µs consumed by the app in the export window (informational; not billed).
   */
  cpu_usec?: number;
  /**
   * Per-(app, month) HTTP response bytes (informational; not billed). It is not collected yet and stays 0.
   */
  tx_bytes?: number;
  /**
   * Per-(app, month) byte delta on root-side vethHost.rx_bytes (informational; not billed). Sourced from the VM manager netstats.Cache via the scheduler ListInstanceStats. Includes Ethernet framing — same kernel counter the per-plan tc tbf qdisc reads.
   */
  net_tx_bytes?: number;
  /**
   * Per-(app, month) byte delta on root-side vethHost.tx_bytes (root→guest = ingress; informational; not billed). Mirror of `net_tx_bytes` for the inbound direction.
   */
  net_rx_bytes?: number;
  /**
   * Per-(app, month) count of WAKE_RESTORE→WAKE_COLD_BOOT transitions observed (informational; not billed).
   */
  cold_boots?: number;
};

