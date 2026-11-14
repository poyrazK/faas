/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { TracingForward } from './TracingForward.js';
/**
 * Opt-in zero-config request tracing baked into each deployment. Managed runtimes export database, cache and HTTP client spans to the debugger without an API key or code changes; apps that configure their own OTel exporter are left untouched.
 */
export type TracingConfig = {
  enabled: boolean;
  /**
   * Fraction of requests whose spans the app exports.
   */
  sample_ratio?: number;
  forward?: TracingForward;
  /**
   * Sends the app's OpenTelemetry metrics to Gregale from inside its instance, stored as its custom metrics (no API key needed). Requires tracing to be enabled and a plan with custom metrics. The managed auto-instrumentation's own metrics count toward the app's metric limit.
   */
  metrics?: boolean;
};

