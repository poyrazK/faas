/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Metadata-only decision from the same transaction as a canary traffic advance. Findings remain in the route result API.
 */
export type RouteGateDecision = {
  mode: 'report' | 'enforce';
  revision: number;
  deployment_id: string;
  status: 'allowed' | 'blocked' | 'report_only';
  reasons: Array<string>;
  check_queued: boolean;
  /**
   * Exact persisted successor-review receipts accepted inside this advance transaction.
   */
  lifecycle_approval_ids?: Array<string>;
};

