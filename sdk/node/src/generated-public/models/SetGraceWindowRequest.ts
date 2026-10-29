/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Body of PATCH /v1/account/keys/grace_window_days. `days` is the per-account override for the rotation grace window. `days=0` is atomic rotation; `days=null` (or omitted) clears the override and falls back to the plan default (api.DefaultAPIKeyGraceWindowDays = 7).
 */
export type SetGraceWindowRequest = {
  /**
   * Per-account override in days. 0 = atomic, null = use plan default.
   */
  days?: number | null;
};

