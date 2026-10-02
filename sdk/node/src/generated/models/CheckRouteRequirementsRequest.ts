/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type CheckRouteRequirementsRequest = {
  deployment_id: string;
  /**
   * Optional saved revision pin; a changed revision returns 409.
   */
  expected_revision?: number;
};

