/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Per-app usage for one month: GB-hours consumed, request count, and an informational CPU-µs field (issue #279 / PR-B). The CPU dimension is observable but not yet billed.
 */
export type UsageResponse = {
  app_id: string;
  mb_seconds: number;
  requests: number;
  included_gb_hours: number;
  /**
   * Cumulative host cgroup CPU-µs (informational; not billed). issue #279 / PR-B.
   */
  cpu_usec?: number;
  /**
   * Per-app monthly HTTP response bytes the gateway forwarded (informational; not billed). Diagnostic subset of canonical net_tx_bytes and never added to it. ADR-046.
   */
  tx_bytes?: number;
  /**
   * Per-app monthly byte delta on root-side vethHost.rx_bytes. Canonical optional egress-billing source; informational while the provider policy is off. ADR-046. Includes Ethernet framing — the same kernel counter used by shaping.
   */
  net_tx_bytes?: number;
  /**
   * Per-app monthly byte delta on root-side vethHost.tx_bytes (root→guest = ingress; informational; not billed). ADR-048. Mirror of `net_tx_bytes` for the inbound direction. Same sysfs source — `/sys/class/net/<vethHost>/statistics/tx_bytes`.
   */
  net_rx_bytes?: number;
  /**
   * Per-app monthly count of WAKE_RESTORE→WAKE_COLD_BOOT transitions observed (informational; not billed). ADR-048. Source: schedd instancestats.Poller → meterd Sampler.
   */
  cold_boots?: number;
};

