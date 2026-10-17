/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AutomationLiveSuiteSummary } from './AutomationLiveSuiteSummary.js';
/**
 * Twenty most recently created suites. One hundred recent suites are retained per automation; clearing saved test data clears shared suite history.
 */
export type ListAutomationLiveSuitesResponse = {
  suites: Array<AutomationLiveSuiteSummary>;
};

