/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { StoredWorkflowLiveBaselineHealth } from './StoredWorkflowLiveBaselineHealth.js';
/**
 * Bounded sorted catalog of active named baseline heads and their current evidence observations.
 */
export type ListStoredWorkflowLiveBaselinesResponse = {
  baselines: Array<StoredWorkflowLiveBaselineHealth>;
  observed_at: string;
};

