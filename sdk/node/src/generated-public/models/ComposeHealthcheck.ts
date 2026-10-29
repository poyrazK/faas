/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Partial Compose override for a prebuilt image HEALTHCHECK. Empty test and zero timing/retry values inherit image settings. NONE disables the check. Durations retain nanosecond precision; positive durations must be at least 1ms.
 */
export type ComposeHealthcheck = {
  /**
   * CMD followed by argv, CMD-SHELL followed by one command, or NONE.
   */
  test?: Array<string>;
  interval_ns?: number;
  timeout_ns?: number;
  start_period_ns?: number;
  start_interval_ns?: number;
  retries?: number;
};

