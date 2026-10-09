/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Current event-triggered workflow and acceptance-time range for a durable historical run-admission job.
 */
export type WorkflowEventReplayBackfillRequest = {
  /**
   * Workflow in the app's preferred live default deployment; its definition is pinned when the job is created.
   */
  workflow_name: string;
  /**
   * Inclusive lower bound on platform event acceptance.
   */
  from: string;
  /**
   * Exclusive acceptance upper bound; future values stop at job creation. At most 30 days are allowed.
   */
  until: string;
};

