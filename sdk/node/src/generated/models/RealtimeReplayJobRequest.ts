/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Realtime Replay Job Request.
 */
export type RealtimeReplayJobRequest = {
  rate: number;
  failure_codes?: Array<string>;
  after_sequence?: string;
  through_sequence?: string;
};

