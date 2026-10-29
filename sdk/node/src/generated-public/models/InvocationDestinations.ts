/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * EPIC #1278. Optional terminal callbacks for an async invocation. Values are app webhook subscription IDs owned by the invoking app.
 */
export type InvocationDestinations = {
  /**
   * Webhook subscription receiving a job.finished event after completion.
   */
  on_success?: string;
  /**
   * Webhook subscription receiving a job.finished event after permanent failure or DLQ routing.
   */
  on_failure?: string;
};

