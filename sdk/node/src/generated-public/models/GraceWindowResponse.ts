/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Body of GET /v1/account/keys/grace_window_days. `days` is the customer's override (null = no override); `plan_default` is the platform default the rotation handler uses when the row is null.
 */
export type GraceWindowResponse = {
  days: number | null;
  /**
   * Platform default grace window in days (api.DefaultAPIKeyGraceWindowDays = 7).
   */
  plan_default: number;
};

