/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowRecoveryFailureEligibility } from './WorkflowRecoveryFailureEligibility.js';
import type { WorkflowRecoveryPolicyAssessment } from './WorkflowRecoveryPolicyAssessment.js';
/**
 * Policy filters applied to a separately timed later observation. Checks expire after 15 minutes; preparation and execution remain unproven. Historical totals never consume these observations.
 */
export type WorkflowRecoveryFailureEligibilityAssessment = {
  observation: WorkflowRecoveryFailureEligibility;
  freshness: 'fresh' | 'stale';
  current: WorkflowRecoveryPolicyAssessment;
  proposed: WorkflowRecoveryPolicyAssessment;
};

