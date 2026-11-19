/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * What to do with the results; commands carry it out. Gregale never ships a variant on its own.
 */
export type FlagExperimentRecommendation = {
  action: 'rolled_back' | 'fix_allocation' | 'ship_variant' | 'keep_control' | 'keep_running' | 'decide';
  /**
   * The variant to ship for ship_variant.
   */
  variant?: string;
  summary: string;
  commands?: Array<string>;
};

