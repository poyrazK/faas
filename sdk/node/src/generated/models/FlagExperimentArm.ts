/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One variant's exposed customers; rate is the conversion rate, or the mean value per customer for mean metrics.
 */
export type FlagExperimentArm = {
  variant: string;
  exposed: number;
  converted: number;
  rate: number;
};

