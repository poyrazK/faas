/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Route requirements gate mode to save after comparing the current gate revision.
 */
export type SetCanaryRouteGateRequest = {
  mode: 'report' | 'enforce';
  expected_revision: number;
};

