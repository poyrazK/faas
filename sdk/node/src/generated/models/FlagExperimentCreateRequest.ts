/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A flag experiment to create.
 */
export type FlagExperimentCreateRequest = {
  key: string;
  /**
   * The variant flag.
   */
  flag: string;
  /**
   * The flag's weighted rule.
   */
  rule_id: string;
  /**
   * The baseline variant.
   */
  control: string;
  /**
   * The conversion event.
   */
  metric_event: string;
  /**
   * mSPRT mixing standard deviation as an absolute conversion-rate lift; default 0.02.
   */
  mixing_lift?: number;
  /**
   * Start the experiment at once.
   */
  start?: boolean;
};

