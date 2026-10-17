/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { IssueImpactSummary } from './IssueImpactSummary.js';
/**
 * Durable failure group across releases in one app and environment.
 */
export type Issue = {
  /**
   * Original fingerprint source for a branch created by an occurrence split rule.
   */
  readonly split_source_issue_id?: string;
  id: string;
  app_id: string;
  environment: string;
  fingerprint: string;
  grouping_version: number;
  title: string;
  state: 'open' | 'resolved' | 'ignored';
  assignee_account_id?: string;
  first_seen_at: string;
  last_seen_at: string;
  event_count: number;
  regression_count: number;
  impact_24h?: IssueImpactSummary;
  resolved_at?: string;
  fixed_deployment_id?: string;
  fixed_deployment_created_at?: string;
  ignored_until?: string;
  /**
   * Current team assignment within this app, mutually exclusive with the account assignee.
   */
  assignee_team_id?: string;
};

