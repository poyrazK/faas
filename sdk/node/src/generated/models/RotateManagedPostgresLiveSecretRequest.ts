/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Live secret rotation. An empty body keeps the previous secret for 24 hours.
 */
export type RotateManagedPostgresLiveSecretRequest = {
  /**
   * How long the previous secret keeps opening seals; 0 revokes it at once.
   */
  overlap_seconds?: number;
};

