/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Worker instance shutdown progress from the deployment audit timeline. Counts are instances, not application tasks. Overdue spans passed their deadline without a recorded outcome.
 */
export type DeploymentWorkerDrainStatus = {
  active: number;
  completed: number;
  timed_out: number;
  failed: number;
  overdue: number;
  /**
   * Earliest unfinished worker drain deadline.
   */
  deadline?: string;
};

