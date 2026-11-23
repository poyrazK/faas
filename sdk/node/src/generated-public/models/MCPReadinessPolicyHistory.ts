/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPReadinessPolicy } from './MCPReadinessPolicy.js';
/**
 * Bounded page of saved environment readiness policy revisions.
 */
export type MCPReadinessPolicyHistory = {
  policies: Array<MCPReadinessPolicy>;
  /**
   * Use as before_revision for the next page; absent on the last page
   */
  next_before_revision?: string;
};

