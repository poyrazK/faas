/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Observed blocked time and interval count for one target, code, contract version and owner within an instance.
 */
export type OperationWorkflowBlockerDuration = {
  contract_version: number;
  operation: string;
  code: string;
  /**
   * Blocker owner observed in this instance interval; empty means unassigned.
   */
  owner: string;
  observed_seconds: number;
  observation_count: number;
  ongoing: boolean;
};

