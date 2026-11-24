/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPReadinessRole } from './MCPReadinessRole.js';
/**
 * Saved environment readiness requirements with revision and audit context.
 */
export type MCPReadinessPolicy = {
  /**
   * Historical source revision for a restore; audit metadata is always newly recorded.
   */
  restored_from?: string;
  /**
   * Environment to which this saved readiness policy applies. Omitted means default.
   */
  scope?: string;
  revision: string;
  enabled: boolean;
  endpoint: string;
  max_age_seconds: number;
  roles: Array<MCPReadinessRole>;
  reason?: string;
  updated_at: string;
  updated_by?: string;
};

