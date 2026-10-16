/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable schedule transition metadata, with receipt identity only for confirmed execution and no gateway-convergence claim.
 */
export type EnvironmentCacheScheduleEvent = {
  sequence: number;
  schedule_id: string;
  kind: 'accepted' | 'rescheduled' | 'cancelled' | 'executed' | 'failed';
  status: 'scheduled' | 'cancelled' | 'executed' | 'failed';
  action: 'policy' | 'inheritance' | 'purge';
  execute_at: string;
  occurred_at: string;
  /**
   * Successor for rescheduled originals or predecessor for accepted successors; absent otherwise.
   */
  related_schedule_id?: string;
  error_code?: string;
  policy_revision?: number;
  purge_generation?: number;
};

