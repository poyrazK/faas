/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AutomationLiveBaselineHealth } from './AutomationLiveBaselineHealth.js';
/**
 * Bounded sorted catalog of active named baseline heads and their current evidence observations.
 */
export type ListAutomationLiveBaselinesResponse = {
  baselines: Array<AutomationLiveBaselineHealth>;
  observed_at: string;
};

