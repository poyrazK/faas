/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { StoredWorkflowTestCaseResult } from './StoredWorkflowTestCaseResult.js';
/**
 * Retained observation, not a publishing receipt. Freshness compares definition and suite content; running does not advance the draft version or perform real actions.
 */
export type StoredWorkflowTestReport = {
  checked_version: number;
  checked_at: string;
  inputs_hash: string;
  passed: boolean;
  cases: Array<StoredWorkflowTestCaseResult>;
};

