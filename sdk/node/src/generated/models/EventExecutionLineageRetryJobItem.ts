/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventExecutionLineageCase } from './EventExecutionLineageCase.js';
import type { EventExecutionLineageRetrySelection } from './EventExecutionLineageRetrySelection.js';
export type EventExecutionLineageRetryJobItem = {
  selection: EventExecutionLineageRetrySelection;
  status: 'pending' | 'changed' | 'unavailable' | 'unresolved' | 'conflict' | 'repaired' | 'already_associated';
  reason?: string;
  result?: EventExecutionLineageCase;
};

