/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable restart evidence. An uncertain teardown is never automatically repeated. A later durable acknowledgement can resume database completion against the original target.
 */
export type OperatorRestartExecutionResponse = {
  phase: 'prepared' | 'started' | 'destroyed' | 'outcome_unknown' | 'completed' | 'failed';
  node_id: string;
  wake_id: string;
  generation: string;
  snapshot_ids: Array<string>;
  destroyed_at?: string;
  updated_at: string;
};

