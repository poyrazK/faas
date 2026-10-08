/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Unsampled calls for one caller → target pair. `errors` are final 5xx
 * responses after proxy retries; identity and authorization failures
 * are excluded. Latency covers successful calls, including routing,
 * wake, and forwarding time.
 *
 */
export type ServiceMapEdge = {
  caller_app_id: string;
  caller_app_slug: string;
  target_app_id: string;
  target_app_slug: string;
  calls: number;
  errors: number;
  error_rate_pct: number;
  latency_p50_ms: number;
  latency_p95_ms: number;
};

