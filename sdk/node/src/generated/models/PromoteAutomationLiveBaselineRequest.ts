/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AutomationLiveBaselinePolicy } from './AutomationLiveBaselinePolicy.js';
export type PromoteAutomationLiveBaselineRequest = {
  suite_id: string;
  expected_revision: number;
  expected_version: number;
  expected_published_version: number;
  expected_deployment_id: string;
  policy: AutomationLiveBaselinePolicy;
};

