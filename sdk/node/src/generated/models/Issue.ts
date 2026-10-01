/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable failure group across releases in one app and environment.
 */
export type Issue = {
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
  resolved_at?: string;
  fixed_deployment_id?: string;
  fixed_deployment_created_at?: string;
  ignored_until?: string;
};

