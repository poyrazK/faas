/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Revision-fenced freshness configuration. Zero disables the target; enabled targets range from 15 to 86400 whole seconds. Existing health webhook destinations receive debounced freshness_unconfirmed incidents.
 */
export type DataPipelineFreshnessRequest = {
  expected_revision: number;
  /**
   * DataPipelineFreshnessRequest uses zero to disable confirmation alerts; enabled targets must be at least 15 seconds.
   */
  target_seconds: number;
};

