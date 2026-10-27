/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowReadinessSnapshot } from './OperationWorkflowReadinessSnapshot.js';
/**
 * Scoped request to reread and compare saved transitions. The baseline is caller-supplied advisory data.
 */
export type OperationWorkflowReadinessComparisonRequest = {
  /**
   * ReadinessComparisonRequest. Required explicit app UUID for customer-self requests; reject on account routes.
   */
  app_id?: string;
  /**
   * ReadinessComparisonRequest. Optional account-owned customer selector; reject on customer-self routes.
   */
  tenant_id?: string;
  /**
   * ReadinessComparisonRequest. Explicit application environment for this comparison.
   */
  scope: string;
  baseline: OperationWorkflowReadinessSnapshot;
  /**
   * ReadinessComparisonRequest. Filter after all baseline transitions have been classified; unavailable includes contract and state boundaries.
   */
  change?: 'newly_ready' | 'newly_blocked' | 'changed' | 'unchanged' | 'unavailable';
  /**
   * ReadinessComparisonRequest. Maximum matching rows returned after comparison; defaults to 20 and is capped at 100.
   */
  limit?: number;
};

