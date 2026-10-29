/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Stable customer identity to which consumer credentials can be attached.
 */
export type CreateAPIConsumerRequest = {
  external_ref: string;
  name: string;
  /**
   * Starts the consumer's one free trial at the next UTC minute for this many days.
   */
  trial_days?: number;
};

