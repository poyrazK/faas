/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Optional loopback callback that must succeed after snapshot restore before the instance becomes ready.
 */
export type AfterRestoreHook = {
  /**
   * Absolute path on the app's loopback HTTP listener. Called with POST after restore, before readiness.
   */
  path?: string;
  /**
   * Callback timeout in milliseconds; 0 uses the 500 ms default.
   */
  timeout_ms?: number;
};

