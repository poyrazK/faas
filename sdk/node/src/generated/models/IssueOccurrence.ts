/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { IssueEvent } from './IssueEvent.js';
import type { IssueGroupingExplanation } from './IssueGroupingExplanation.js';
/**
 * Retained sanitized occurrence with verified platform attribution and optional debugger correlation.
 */
export type IssueOccurrence = (IssueEvent & {
  id?: string;
  /**
   * Original fingerprint provenance retained through occurrence-level splits and manual merges.
   */
  readonly original_issue_id?: string;
  deployment_id: string;
  received_at: string;
  verified_consumer_id?: string;
  verified_platform_tenant_id?: string;
  debug_request_id?: string;
  attribution: string;
  grouping_explanation?: IssueGroupingExplanation;
});

