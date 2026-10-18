/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { IssueCompoundAlertRule } from './IssueCompoundAlertRule.js';
/**
 * Replacement compound-alert rules guarded by the expected policy revision.
 */
export type UpdateIssueCompoundAlertPolicyRequest = {
  expected_revision: number;
  rules: Array<IssueCompoundAlertRule>;
};

