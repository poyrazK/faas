/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Why the next breaking candidate may ship.
 */
export type CreateMCPContractOverrideRequest = {
  reason: string;
  /**
   * Defaults to 3600.
   */
  expires_in_seconds?: number;
  /**
   * Bind the override to one of the app's deployments; without it the app's next breaking production candidate uses it, or an unverified one late in its recovery window.
   */
  deployment_id?: string;
};

