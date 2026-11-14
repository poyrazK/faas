/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Read-time confirmation of an applied checkpoint against the owner freshness target. Old confirmation is not measured replication lag; an idle source may already be caught up.
 */
export type DataPipelineFreshness = {
  /**
   * DataPipelineFreshness uses zero to disable confirmation alerts; enabled targets must be at least 15 seconds.
   */
  target_seconds: number;
  state: string;
  confirmed_through_at?: string;
  confirmation_age_seconds?: number;
  message: string;
};

