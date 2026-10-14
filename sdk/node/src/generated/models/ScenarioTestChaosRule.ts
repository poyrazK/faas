/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One bounded HTTP request, private-service DNS lookup, TCP stream, or new TCP connection fault for matching scenario workloads.
 */
export type ScenarioTestChaosRule = {
  /**
   * Optional strict per-request HTTP attempt scope; requires a full-coverage latency/status rule with an explicit source.
   */
  correlation_scope?: string;
  from?: string;
  to: string;
  kind: 'latency' | 'http_status' | 'http_malformed_json' | 'http_truncated_body' | 'http_response_lost' | 'dns_latency' | 'dns_timeout' | 'dns_servfail' | 'dns_nxdomain' | 'tcp_latency' | 'tcp_bandwidth' | 'tcp_timeout' | 'tcp_reset' | 'tcp_connect_timeout' | 'tcp_connect_refused';
  percent: number;
  latency_ms?: number;
  /**
   * Synthetic HTTP status; accepts 429 or 500 through 599.
   */
  status_code?: (429 | number);
  /**
   * Optional Retry-After delta-seconds header. Only valid for http_status with status_code 429 or 503.
   */
  retry_after_seconds?: number;
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

