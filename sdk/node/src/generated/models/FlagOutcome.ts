/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Request-weighted observations for one boolean value or named variant. Boolean values are encoded as the strings "true" and "false".
 */
export type FlagOutcome = {
  type: 'boolean' | 'variant';
  /**
   * Boolean value "true"/"false" or the selected variant key.
   */
  value: string;
  request_count: number;
  /**
   * Application-reported requests where the decision was marked used.
   */
  used_count: number;
  http_5xx_count: number;
  /**
   * HTTP 5xx request count divided by request_count.
   */
  http_5xx_rate: number;
  /**
   * Request-weighted nearest-rank p50 from stored latency bucket upper bounds.
   */
  p50_latency_ms: number;
  /**
   * Request-weighted nearest-rank p95 from stored latency bucket upper bounds.
   */
  p95_latency_ms: number;
  /**
   * Latency values are conservative bucket upper bounds.
   */
  latency_quantized: boolean;
};

