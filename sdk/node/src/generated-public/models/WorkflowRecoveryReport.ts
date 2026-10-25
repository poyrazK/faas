/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowRecoveryHistoryEntry } from './WorkflowRecoveryHistoryEntry.js';
import type { WorkflowRecoveryReportBlocker } from './WorkflowRecoveryReportBlocker.js';
import type { WorkflowRecoveryReportGroup } from './WorkflowRecoveryReportGroup.js';
import type { WorkflowRecoveryReportTotals } from './WorkflowRecoveryReportTotals.js';
/**
 * Observed recovery counts with explicit retention and scan coverage limitations.
 */
export type WorkflowRecoveryReport = {
  observed_at: string;
  created_after: string;
  created_before: string;
  scanned_entries: number;
  durable_entries: number;
  truncated: boolean;
  coverage_notes: Array<string>;
  totals: WorkflowRecoveryReportTotals;
  groups: Array<WorkflowRecoveryReportGroup>;
  blockers: Array<WorkflowRecoveryReportBlocker>;
  evidence: Array<WorkflowRecoveryHistoryEntry>;
};

