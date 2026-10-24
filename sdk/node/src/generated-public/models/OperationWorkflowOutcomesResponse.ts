/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowOutcomeEntry } from './OperationWorkflowOutcomeEntry.js';
/**
 * Paginated collection of matching terminal workflow outcomes evaluated at a shared time.
 */
export type OperationWorkflowOutcomesResponse = {
  items: Array<OperationWorkflowOutcomeEntry>;
  evaluated_at: string;
  next_cursor?: string;
};

