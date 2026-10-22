/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * ADR-1196 bounds fixed onto each accepted Operation. Every field is optional and bounded by the plan; workflow definitions take a deadline and an execution budget (ADR-20261016110803), and job definitions take none.
 */
export type OperationLimits = {
  /**
   * Wall clock from acceptance, including waits, retries and recovery.
   */
  deadline_seconds?: number;
  /**
   * Automatic attempts of one HTTP execution.
   */
  max_attempts?: number;
  /**
   * Runtime of HTTP attempts, or of a workflow's HTTP step attempts, summed across generations; waits do not count.
   */
  max_execution_seconds?: number;
};

