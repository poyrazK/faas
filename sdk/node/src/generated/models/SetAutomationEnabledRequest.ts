/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Publication revision and the desired state of automatic starts.
 */
export type SetAutomationEnabledRequest = {
  /**
   * Current automation revision to pause or resume.
   */
  expected_version: number;
  enabled: boolean;
};

