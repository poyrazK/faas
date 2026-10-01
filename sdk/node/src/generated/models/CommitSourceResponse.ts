/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type CommitSourceResponse = {
  id: string;
  app_id: string;
  name: string;
  enabled: boolean;
  relay_status?: string;
  last_checked_at?: string;
  pending_events?: number;
  blocked_events?: number;
  oldest_pending_at?: string;
};

