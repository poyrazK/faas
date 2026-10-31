/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Consecutive qualified observations for one configured route in the current canary stage.
 */
export type ProfileGateRouteStreak = {
  route: string;
  status: 'regressed' | 'no_regression_detected' | 'insufficient_data';
  count: number;
};

