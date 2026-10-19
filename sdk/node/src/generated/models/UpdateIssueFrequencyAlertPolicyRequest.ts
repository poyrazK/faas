/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { IssueFrequencyAlertRule } from './IssueFrequencyAlertRule.js';
/**
 * Replacement frequency-alert rules guarded by the expected policy revision.
 */
export type UpdateIssueFrequencyAlertPolicyRequest = {
  expected_revision: number;
  rules: Array<IssueFrequencyAlertRule>;
};

