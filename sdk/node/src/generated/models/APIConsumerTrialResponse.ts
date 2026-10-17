/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A consumer's free trial: usage in [starts_at, ends_at) is priced at zero.
 */
export type APIConsumerTrialResponse = {
  consumer_id: string;
  starts_at: string;
  ends_at: string;
  /**
   * False once the trial has ended.
   */
  active: boolean;
};

