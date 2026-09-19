/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Active image-scan quarantine details. Vulnerability findings remain on the deployment scan resource.
 */
export type AppSecurityQuarantine = {
  /**
   * Deployment whose scan regression caused the quarantine.
   */
  deployment_id: string;
  /**
   * Immutable image digest that was quarantined.
   */
  image_digest: string;
  reason: 'security_scan_regressed';
  parked_at?: string | null;
};

