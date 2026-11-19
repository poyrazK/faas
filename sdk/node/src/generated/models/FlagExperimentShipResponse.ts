/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FlagExperiment } from './FlagExperiment.js';
/**
 * A shipped experiment.
 */
export type FlagExperimentShipResponse = {
  experiment: FlagExperiment;
  /**
   * The flag configuration version that serves the variant.
   */
  config_version: number;
};

