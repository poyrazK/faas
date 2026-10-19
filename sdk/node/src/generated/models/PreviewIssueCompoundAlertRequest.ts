/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { IssueCompoundAlertRule } from './IssueCompoundAlertRule.js';
/**
 * An unsaved compound rule and an existing issue to evaluate without notification side effects.
 */
export type PreviewIssueCompoundAlertRequest = {
  issue_id: string;
  rule: IssueCompoundAlertRule;
};

