/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ScheduleOccurrenceResponse } from './ScheduleOccurrenceResponse.js';
/**
 * Newest-first page of scheduled occurrence decisions.
 */
export type ListScheduleOccurrencesResponse = {
  occurrences: Array<ScheduleOccurrenceResponse>;
  limit: number;
  before?: string;
  /**
   * Pass this id as before to read an older page; omitted when the page is complete.
   */
  next_before?: string;
};

