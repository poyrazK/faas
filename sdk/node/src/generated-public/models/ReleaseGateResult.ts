/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One judged release gate in a rollout decision. Aggregates only: no routes, messages or metric labels.
 */
export type ReleaseGateResult = {
  name: string;
  kind: 'slo' | 'new_errors' | 'custom_metric' | 'ai';
  passed: boolean;
  /**
   * The burn rate, new error groups, metric average, or the release's AI metric.
   */
  observed: number;
  /**
   * The value that fails the gate.
   */
  bound: number;
  /**
   * The release's requests, new-error events, metric values or AI calls.
   */
  samples: number;
  /**
   * slo gates: the predecessor's bad fraction.
   */
  baseline_bad_fraction?: number;
  /**
   * new_errors gates: the new groups' error classes.
   */
  error_classes?: Array<string>;
  /**
   * What the gate measured, in one line.
   */
  summary: string;
};

