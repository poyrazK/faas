/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { SidecarExecProbe } from './SidecarExecProbe.js';
import type { SidecarHTTPGetProbe } from './SidecarHTTPGetProbe.js';
import type { SidecarTCPSocketProbe } from './SidecarTCPSocketProbe.js';
/**
 * Container-local startup or liveness probe for a companion. Specify
 * exactly one action: exec, http_get, tcp_socket, or the legacy OCI
 * test field. Port 0/omitted uses the workload's declared port, then
 * the image port, then the platform default.
 *
 */
export type SidecarProbe = {
  /**
   * Legacy OCI exec form: CMD, CMD-SHELL, or NONE. Retained for startup_probe compatibility.
   */
  test?: Array<string>;
  exec?: SidecarExecProbe;
  http_get?: SidecarHTTPGetProbe;
  tcp_socket?: SidecarTCPSocketProbe;
  /**
   * Probe interval in seconds; typed-probe default 10, legacy OCI default 30.
   */
  period_s?: number;
  /**
   * Legacy alias for period_s.
   */
  interval_s?: number;
  /**
   * Probe timeout in seconds; defaults to 1 for typed probes and 30 for legacy OCI probes.
   */
  timeout_s?: number;
  /**
   * Consecutive failures before startup fails or liveness restarts the workload; defaults to 3.
   */
  failure_threshold?: number;
  /**
   * Legacy alias for failure_threshold.
   */
  retries?: number;
  /**
   * Consecutive passes required to become healthy; defaults to 1.
   */
  success_threshold?: number;
  /**
   * Delay before the first probe.
   */
  initial_delay_s?: number;
  /**
   * Legacy OCI liveness grace period during which failures do not count.
   */
  start_period_s?: number;
};

