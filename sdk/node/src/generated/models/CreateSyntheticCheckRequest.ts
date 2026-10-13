/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Defines a scheduled HTTP check against the app (ADR-748).
 */
export type CreateSyntheticCheckRequest = {
  /**
   * Unique per app.
   */
  name: string;
  /**
   * HTTP method of the probe.
   */
  method?: 'GET' | 'HEAD';
  /**
   * Origin-relative path, query string allowed. The host is always the app's own default hostname.
   */
  path: string;
  /**
   * Exact status that counts as success; omit to accept any 2xx. Redirects are not followed.
   */
  expected_status?: number;
  /**
   * Whole-request timeout, including any wake of a parked app.
   */
  timeout_ms?: number;
  /**
   * How often the check runs. Five minutes is the floor so a check never keeps an app permanently awake.
   */
  interval_seconds: 300 | 900 | 3600;
};

