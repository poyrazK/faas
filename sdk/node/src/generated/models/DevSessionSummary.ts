/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One developer environment in the account list.
 */
export type DevSessionSummary = {
  project: string;
  /**
   * Backing dev-* app slug; DELETE /v1/dev/sessions/{project}?app= stops it.
   */
  app: string;
  url: string;
  /**
   * Lease state: open, stale, tearing_down or torn_down.
   */
  state?: string;
  expires_at: string;
};

