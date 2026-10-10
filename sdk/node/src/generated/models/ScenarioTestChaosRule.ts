/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One bounded HTTP request, TCP stream, or new TCP connection fault for matching internal scenario services.
 */
export type ScenarioTestChaosRule = {
  from?: string;
  to: string;
  kind: 'latency' | 'http_status' | 'tcp_latency' | 'tcp_bandwidth' | 'tcp_timeout' | 'tcp_reset' | 'tcp_connect_timeout' | 'tcp_connect_refused';
  percent: number;
  latency_ms?: number;
  status_code?: number;
  seed: number;
  /**
   * Required target port for TCP rules.
   */
  port?: number;
  /**
   * TCP direction relative to the calling application. Defaults to both; connection faults require both.
   */
  direction?: 'upstream' | 'downstream' | 'both';
  /**
   * Required per-connection directional throughput cap for tcp_bandwidth.
   */
  rate_kib_per_second?: number;
  /**
   * Delay before tcp_reset. Defaults to an immediate reset.
   */
  reset_after_ms?: number;
};

