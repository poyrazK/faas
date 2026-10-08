/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The acknowledged entity result. Replay returns the original value and version.
 */
export type DurableEntityInvokeResponse = {
  /**
   * JSON result from the committed transition.
   */
  value: any;
  /**
   * Committed transition version, or original version on replay.
   */
  version: number;
  /**
   * True when an existing receipt supplied the result.
   */
  replayed: boolean;
};

