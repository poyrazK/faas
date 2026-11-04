/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfileCallPathFrame } from './ProfileCallPathFrame.js';
import type { ProfileRegressionMetric } from './ProfileRegressionMetric.js';
/**
 * Comparable function self CPU or complete caller-path inclusive CPU meeting both thresholds. Inclusive path entries overlap and must not be summed. Frame names and source paths are observed profile symbols; use an authorized profile comparison to inspect source when available.
 */
export type ProfileRegressionEvidence = {
  kind: 'function' | 'call_path';
  frames: Array<ProfileCallPathFrame>;
  metric: ProfileRegressionMetric;
};

