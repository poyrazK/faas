/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPReadinessRole } from './MCPReadinessRole.js';
/**
 * Replacement readiness configuration with expected-revision concurrency control.
 */
export type SetMCPReadinessPolicyRequest = {
  /**
   * Environment whose readiness policy is replaced by this request. Omit for default.
   */
  scope?: string;
  expected_revision: string;
  enabled: boolean;
  endpoint: string;
  max_age_seconds: number;
  roles: Array<MCPReadinessRole>;
  reason: string;
};

