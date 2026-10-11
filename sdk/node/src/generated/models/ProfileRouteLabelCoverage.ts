/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Application-reported labeled request entries in retained whole captures divided by weighted observed requests in the deployment window. Boundary captures are excluded without extrapolation. This is not proof of full request instrumentation. Counts above observed traffic cannot be reconciled.
 */
export type ProfileRouteLabelCoverage = {
  available: boolean;
  reason: string;
  labeled_requests?: number;
  observed_requests?: number;
  percent?: number;
  captured_profiles: number;
  boundary_profiles: number;
};

