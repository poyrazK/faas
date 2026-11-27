/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { InsightStepRequest } from './InsightStepRequest.js';
/**
 * A funnel metric: a customer converts when it completes the steps in order within the window after its first exposure.
 */
export type FlagExperimentFunnel = {
  steps: Array<InsightStepRequest>;
  /**
   * Go duration from first exposure, 1h to 720h.
   */
  window: string;
};

