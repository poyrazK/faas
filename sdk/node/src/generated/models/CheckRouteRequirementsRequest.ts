/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * App-owned deployment capture to evaluate against saved route intent, with an optional intent revision pin.
 */
export type CheckRouteRequirementsRequest = {
  deployment_id: string;
  /**
   * Optional saved revision pin; a changed revision returns 409.
   */
  expected_revision?: number;
};

