/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Exact normalized telemetry operation selected for canary error checks and optional latency checks.
 */
export type RouteHealthRoute = {
  method: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE' | 'HEAD' | 'OPTIONS';
  /**
   * Exact gateway-normalized telemetry path without method prefix, query, fragment or wildcard. For example /profiles/{id}.
   */
  path: string;
  /**
   * Opt in to the relative p95 slowdown check (at least 1.5 times stable and 100 ms slower). Omitted or false disables this check independently of max_p95_ms.
   */
  check_latency?: boolean;
  /**
   * Absolute candidate p95 latency budget in milliseconds. A positive value enables this check; omitted or zero disables it. Does not enable the relative slowdown check.
   */
  max_p95_ms?: number;
};

