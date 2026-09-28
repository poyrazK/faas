/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Safe status of the latest deployment attempt for the surface's app. This is not a claim that the attempt is currently serving; IDs, source metadata, logs, and errors are omitted.
 */
export type PlatformTenantSelfDeploymentResponse = {
  status: 'pending' | 'building' | 'imaging' | 'snapshotting' | 'live' | 'failed' | 'superseded' | 'cancelled';
  /**
   * Positive app revision when assigned.
   */
  revision?: number;
  started_at: string;
};

