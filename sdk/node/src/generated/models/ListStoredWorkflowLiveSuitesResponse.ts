/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { StoredWorkflowLiveSuiteSummary } from './StoredWorkflowLiveSuiteSummary.js';
/**
 * Twenty most recently created suites. One hundred recent suites are retained per stored workflow; clearing saved test data clears shared suite history.
 */
export type ListStoredWorkflowLiveSuitesResponse = {
  suites: Array<StoredWorkflowLiveSuiteSummary>;
};

