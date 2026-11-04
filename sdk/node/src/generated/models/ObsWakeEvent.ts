/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One events row of a wake.
 */
export type ObsWakeEvent = {
  at: string;
  kind: string;
  actor?: string;
  subject?: string;
  trace_id?: string;
  /**
   * The event's payload as recorded.
   */
  data?: Record<string, any>;
};

