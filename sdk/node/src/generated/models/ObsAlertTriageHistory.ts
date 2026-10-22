/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsAlertTriageHistoryCoverage } from './ObsAlertTriageHistoryCoverage.js';
import type { ObsAlertTriageHistoryEntry } from './ObsAlertTriageHistoryEntry.js';
/**
 * One episode-bound page of workflow receipts with a fixed upper revision and explicit retention gaps.
 */
export type ObsAlertTriageHistory = {
  incident_id: string;
  generated_at: string;
  /**
   * First page clock retained across cursor requests; it is not a persistent database snapshot.
   */
  snapshot_at: string;
  snapshot_revision: number;
  /**
   * Current episode revision in this page read; can exceed snapshot_revision on subsequent pages.
   */
  current_revision: number;
  items: Array<ObsAlertTriageHistoryEntry>;
  limit: number;
  truncated: boolean;
  next_cursor: string;
  history_path: string;
  coverage: ObsAlertTriageHistoryCoverage;
};

