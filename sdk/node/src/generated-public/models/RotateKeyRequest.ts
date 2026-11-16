/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Optional body of POST /v1/keys/{id}/rotate. `grace_window_days` overrides, for this rotation only, how many days the old key keeps working; `0` revokes it at once (use it for a leaked key). Omitted uses the account's grace window or the plan default (7 days).
 */
export type RotateKeyRequest = {
  /**
   * Days the old key keeps working after this rotation. `0` revokes it at once.
   */
  grace_window_days?: number;
};

