/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventExecutionLineageCase } from './EventExecutionLineageCase.js';
/**
 * Scoped page of retained execution-lineage diagnostics with a continuation boundary.
 */
export type EventExecutionLineageCases = {
  cases: Array<EventExecutionLineageCase>;
  next_after?: number;
};

