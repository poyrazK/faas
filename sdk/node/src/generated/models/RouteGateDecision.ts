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
  reasons: Array<'evidence_unavailable' | 'check_missing' | 'check_incomplete' | 'check_stale' | 'verdict_unknown' | 'requirements_violated' | 'use_canary_advance'>;
  check_queued: boolean;
};

