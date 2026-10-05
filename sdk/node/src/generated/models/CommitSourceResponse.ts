/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Commit source destination, enabled state and latest bounded relay health observation.
 */
export type CommitSourceResponse = {
  id: string;
  app_id: string;
  name: string;
  enabled: boolean;
  /**
   * Immutable managed Operations policy. Absent only for legacy internal sources.
   */
  operation_policy?: string;
  contract_version: 1 | 2;
  allow_tenant_selection: boolean;
  relay_status?: string;
  last_checked_at?: string;
  pending_events?: number;
  blocked_events?: number;
  oldest_pending_at?: string;
};

