/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DevSyncHistoryItem } from './DevSyncHistoryItem.js';
import type { DevSyncHistorySummary } from './DevSyncHistorySummary.js';
/**
 * Bounded newest-first developer sync history.
 */
export type DevSyncHistoryResponse = {
  project: string;
  workspace_id?: string;
  items: Array<DevSyncHistoryItem>;
  summary?: DevSyncHistorySummary;
};

