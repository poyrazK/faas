/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowRetainedMilestoneRequirement } from './OperationWorkflowRetainedMilestoneRequirement.js';
/**
 * Versioned maximum age for one declared mandatory or alternative retained prerequisite; measured from business occurrence time.
 */
export type OperationWorkflowRetainedMilestoneFreshnessRule = {
  prerequisite: OperationWorkflowRetainedMilestoneRequirement;
  max_age_seconds: number;
};

