/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FlagExperimentLayerSlice } from './FlagExperimentLayerSlice.js';
/**
 * An experiment layer and the slices its running experiments hold.
 */
export type FlagExperimentLayer = {
  key: string;
  slices: Array<FlagExperimentLayerSlice>;
  /**
   * Basis points no experiment holds.
   */
  free: number;
};

