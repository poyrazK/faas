/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A consumer's trial length in days from its start, or its end (ADR-1314). Set exactly one.
 */
export type PutAPIConsumerTrialRequest = {
  days?: number;
  /**
   * A UTC minute from the next minute on.
   */
  ends_at?: string;
};

