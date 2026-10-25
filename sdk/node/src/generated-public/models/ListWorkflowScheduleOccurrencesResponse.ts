/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowScheduleOccurrenceResponse } from './WorkflowScheduleOccurrenceResponse.js';
/**
 * Page of due workflow schedule outcomes ordered by nominal minute, with a cursor for the next page.
 */
export type ListWorkflowScheduleOccurrencesResponse = {
  occurrences: Array<WorkflowScheduleOccurrenceResponse>;
  next_cursor?: string;
};

