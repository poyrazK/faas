/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { JobTaskAttemptResponse } from './JobTaskAttemptResponse.js';
/**
 * A page of task attempts in attempt order.
 */
export type ListJobTaskAttemptsResponse = {
  attempts: Array<JobTaskAttemptResponse>;
  limit: number;
  offset: number;
  next_offset: number;
};

