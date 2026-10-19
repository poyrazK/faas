/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPReadinessCheck } from './MCPReadinessCheck.js';
/**
 * Platform-recorded readiness results bound to deployment configuration and probe policy.
 */
export type MCPReadinessEvidence = {
  id: string;
  deployment_id: string;
  role: string;
  policy_revision: string;
  artifact_sha256: string;
  protocol_version: string;
  catalog_format: number;
  catalog_sha256?: string;
  passed: boolean;
  observed_at: string;
  checks: Array<MCPReadinessCheck>;
};

