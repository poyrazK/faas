/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationSummary } from './OperationSummary.js';
/**
 * Bounded live discovery page of operation summaries, scoped to a verified customer or account operator.
 */
export type OperationListResponse = {
  operations: Array<OperationSummary>;
  next_cursor?: string;
};

