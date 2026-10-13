/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Realtime Dead Letter Preview Request.
 */
export type RealtimeDeadLetterPreviewRequest = {
  /**
   * Empty selects all codes; otherwise exact case-sensitive matches.
   */
  failure_codes?: Array<string>;
  /**
   * Exclusive lower bound within signed 64-bit sequence range.
   */
  after_sequence?: string;
  /**
   * Optional inclusive upper bound greater than after_sequence.
   */
  through_sequence?: string;
  limit?: number;
};

