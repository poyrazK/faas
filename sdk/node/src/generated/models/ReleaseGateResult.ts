/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One judged release gate in a rollout decision. Aggregates only: no routes, messages or metric labels.
 */
export type ReleaseGateResult = {
  name: string;
  kind: 'slo' | 'new_errors' | 'custom_metric' | 'synthetic' | 'metric_change';
  passed: boolean;
  /**
   * The burn rate, new error groups, metric average, failed-probe percentage, or signed change in percent.
   */
  observed: number;
  /**
   * The value that fails the gate.
   */
  bound: number;
  /**
   * The release's requests, new-error events, probes, or metric values.
   */
  samples: number;
  /**
   * slo gates: the predecessor's bad fraction.
   */
  baseline_bad_fraction?: number;
  /**
   * synthetic gates: the stable revision's failed-probe percentage; metric_change gates: the metric's average before the rollout.
   */
  baseline?: number;
  /**
   * new_errors gates: the new groups' error classes.
   */
  error_classes?: Array<string>;
  /**
   * What the gate measured, in one line.
   */
  summary: string;
};

