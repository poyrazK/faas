/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Opt-in zero-config request tracing baked into each deployment (ADR-958). Managed runtimes export database, cache and HTTP client spans to the debugger without an API key or code changes; apps that configure their own OTel exporter are left untouched.
 */
export type TracingConfig = {
  enabled: boolean;
  /**
   * Fraction of requests whose spans the app exports.
   */
  sample_ratio?: number;
};

