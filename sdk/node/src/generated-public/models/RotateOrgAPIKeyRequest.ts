/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * POST /v1/orgs/{slug}/keys/{id}/rotate body. `label` overrides the new key's label (inherits from the predecessor when omitted); `grace_window_days` is the same per-account override as `PATCH /v1/account/keys/grace_window_days` — defaulting to the plan default when omitted (`api.DefaultAPIKeyGraceWindowDays = 7`).
 */
export type RotateOrgAPIKeyRequest = {
  label?: string;
  /**
   * Days the predecessor stays valid after rotation. `0` is atomic; omitted/null falls back to the plan default.
   */
  grace_window_days?: number;
};

