/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Retained audited readiness override for a deployment.
 */
export type MCPReadinessOverride = {
  id: string;
  deployment_id: string;
  policy_revision: string;
  artifact_sha256: string;
  reason: string;
  created_by: string;
  created_at: string;
  expires_at: string;
  used_at?: string;
};

