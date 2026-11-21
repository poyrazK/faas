/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RunHistoryEntry } from './RunHistoryEntry.js';
/**
 * Filtered page of authorized invocation records and its continuation cursor.
 */
export type RunHistoryResponse = {
  runs: Array<RunHistoryEntry>;
  next_cursor?: string;
};

