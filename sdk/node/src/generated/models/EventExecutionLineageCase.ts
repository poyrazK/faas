/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Retained reason-coded lineage diagnostic with revision and incarnation guards for reevaluation.
 */
export type EventExecutionLineageCase = {
  /**
   * Unique incarnation of a diagnostically created case; required in its retry guards.
   */
  diagnostic_id?: string;
  subscription_id: string;
  app_id: string;
  outbox_id: number;
  accepted_at: string;
  root_invocation_id?: string;
  root_created_at?: string;
  status: 'unresolved' | 'conflict' | 'repaired' | 'already_associated';
  reason: string;
  revision: number;
  retry_count: number;
  recorded_at: string;
  retain_until: string;
};

