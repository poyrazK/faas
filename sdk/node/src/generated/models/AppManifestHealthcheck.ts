/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { SidecarExecProbe } from './SidecarExecProbe.js';
import type { SidecarHTTPGetProbe } from './SidecarHTTPGetProbe.js';
import type { SidecarTCPSocketProbe } from './SidecarTCPSocketProbe.js';
/**
 * AppManifest-level healthcheck shape: OCI HEALTHCHECK fields plus typed deployment probe overrides. Durations are integer seconds at the JSON boundary to match OCI/Docker conventions.
 */
export type AppManifestHealthcheck = {
  /**
   * Argv of the check command, prefixed by "CMD", "CMD-SHELL", or "NONE" per Docker semantics.
   */
  test?: Array<string>;
  exec?: SidecarExecProbe;
  http_get?: SidecarHTTPGetProbe;
  tcp_socket?: SidecarTCPSocketProbe;
  /**
   * Typed sidecar probe cadence in seconds; defaults to 10, or 30 for legacy OCI checks.
   */
  period_s?: number;
  /**
   * Poll cadence after StartPeriodS elapses (Docker default 30s).
   */
  interval_s?: number | null;
  /**
   * Per-probe exec timeout (Docker default 30s).
   */
  timeout_s?: number | null;
  /**
   * Consecutive failure count to mark unhealthy (Docker default 3).
   */
  retries?: number | null;
  /**
   * Failure count that marks a startup probe failed or restarts a liveness workload; defaults to 3.
   */
  failure_threshold?: number;
  /**
   * Consecutive passes required before the probe reports healthy; defaults to 1.
   */
  success_threshold?: number;
  /**
   * Seconds to wait before the first typed sidecar probe.
   */
  initial_delay_s?: number;
  /**
   * Startup grace during which failures don't count (Docker 17.05+, default 0s).
   */
  start_period_s?: number | null;
};

