/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The running experiment of a weighted rule, set when the experiment starts and cleared when it stops. Apid writes it; a configuration update must carry it unchanged. Runtime SDKs need capabilities experiment-layers-v1 and subject-exposures-v1.
 */
export type FlagRuleExperiment = {
  /**
   * Experiment key.
   */
  key: string;
  /**
   * The variant held-out customers see.
   */
  control: string;
  unit: 'customer' | 'subject';
  /**
   * Exclusive layer whose slice [from, to) the experiment holds.
   */
  layer?: string;
  from?: number;
  to?: number;
};

