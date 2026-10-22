/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Adds a release gate. Give exactly one kind's fields; omitted numbers take Gregale's defaults.
 */
export type CreateReleaseGateRequest = {
  name: string;
  kind: 'slo' | 'new_errors' | 'custom_metric';
  /**
   * slo gates: the name of one of the app's SLOs.
   */
  slo?: string;
  /**
   * slo gates: default 10.
   */
  max_burn_rate?: number;
  /**
   * new_errors gates: default 0.
   */
  max_new_groups?: number;
  /**
   * new_errors gates: default 3.
   */
  min_events?: number;
  /**
   * custom_metric gates: the custom metric's name.
   */
  metric?: string;
  /**
   * custom_metric gates: fail when the average is below or above the threshold.
   */
  comparison?: 'below' | 'above';
  /**
   * custom_metric gates: the bound.
   */
  threshold?: number;
  /**
   * Default 20.
   */
  min_samples?: number;
};

