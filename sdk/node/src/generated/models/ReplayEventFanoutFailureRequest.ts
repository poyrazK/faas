/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Identity of one terminal fanout failure to replay.
 */
export type ReplayEventFanoutFailureRequest = {
  /**
   * Allow expired in the replay event fanout failure request: explicitly override delivery age for this replay generation or historical backfill job. Preserves deterministic invocation identity and manual controls.
   */
  allow_expired?: boolean;
  event_id: string;
  event_source: string;
  subscription_id: string;
};

