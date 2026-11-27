/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { StoredWorkflowLiveBaselinePolicy } from './StoredWorkflowLiveBaselinePolicy.js';
/**
 * Explicit target suite, reviewed policy and expected baseline revision for an audited promotion.
 */
export type PromoteStoredWorkflowLiveBaselineRequest = {
  suite_id: string;
  expected_revision: number;
  expected_version: number;
  expected_published_version: number;
  expected_deployment_id: string;
  policy: StoredWorkflowLiveBaselinePolicy;
};

