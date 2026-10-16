/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * An operator decision fenced against the inspected progressive checkpoint.
 */
export type ProjectReleaseControlRequest = {
  /**
   * Reuse this identity after an ambiguous response.
   */
  request_id: string;
  /**
   * Inspected checkpoint generation.
   */
  expected_generation: number;
};

