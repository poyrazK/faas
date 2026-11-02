/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PublishEventBatchResult } from './PublishEventBatchResult.js';
/**
 * Per-event outcomes in input order, with the app source the events published under.
 */
export type DataAPIChangeEventBatchResponse = {
  /**
   * The app's producer source
   */
  source: string;
  results: Array<PublishEventBatchResult>;
};

