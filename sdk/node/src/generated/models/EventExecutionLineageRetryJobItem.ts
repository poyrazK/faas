/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventExecutionLineageCase } from './EventExecutionLineageCase.js';
import type { EventExecutionLineageRetrySelection } from './EventExecutionLineageRetrySelection.js';
/**
 * Retained evaluation status and guarded retry result for one selected case in a bulk job.
 */
export type EventExecutionLineageRetryJobItem = {
  selection: EventExecutionLineageRetrySelection;
  status: 'pending' | 'cancelled' | 'changed' | 'unavailable' | 'unresolved' | 'conflict' | 'repaired' | 'already_associated';
  reason?: string;
  result?: EventExecutionLineageCase;
};

