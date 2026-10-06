/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Restores a selected immutable publication into the draft without publishing it.
 */
export type RestoreAutomationRevisionRequest = {
  /**
   * Current draft revision for optimistic concurrency; zero creates a draft after deletion.
   */
  expected_version: number;
};

