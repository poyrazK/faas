/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable candidate graph and expected routing and policy state. This manifest conveys no evidence or waiver.
 */
export type CheckedProjectReleaseManifest = {
  /**
   * Explicit intent to return to checked weighted fallback routes; requires the deactivation endpoint.
   */
  deactivate?: boolean;
  /**
   * Historical source identity; requires the checked rollback endpoint.
   */
  rollback_release_id?: string;
  project: string;
  environment: string;
  /**
   * Zero for deactivation; positive retention TTL for activation or rollback.
   */
  ttl_seconds: number;
  deployments: Record<string, string>;
  /**
   * Expected active release UUID or an empty string for the first graph.
   */
  expected_active_release_id: string;
  expected_fallback_deployments: Record<string, string>;
  expected_policy_revisions: Record<string, number>;
  service_targets: Record<string, Record<string, string>>;
};

