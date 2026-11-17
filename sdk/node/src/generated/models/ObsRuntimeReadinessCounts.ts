/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Readiness states partition the full inspected set before preview truncation.
 */
export type ObsRuntimeReadinessCounts = {
  ready: number;
  not_ready: number;
  missing: number;
  stale: number;
  conflicting: number;
  unavailable: number;
};

