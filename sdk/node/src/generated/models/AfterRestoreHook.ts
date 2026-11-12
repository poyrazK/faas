/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Optional loopback callback that must succeed after snapshot restore, or after a frozen instance resumes, before the instance becomes ready.
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
  /**
   * reload lets an environment branch wake from the parent deployment's credential-free capture instead of a cold boot; the callback then re-reads config and reconnects under the branch's identity. Omit to cold-boot branches.
   */
  identity?: 'reload';
};

