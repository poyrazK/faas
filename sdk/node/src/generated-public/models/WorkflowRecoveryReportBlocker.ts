/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowRecoveryHistoryEntry } from './WorkflowRecoveryHistoryEntry.js';
/**
 * Distinct actions affected by an observed worker blocker with bounded source evidence.
 */
export type WorkflowRecoveryReportBlocker = {
  reason: string;
  actions: number;
  evidence: Array<WorkflowRecoveryHistoryEntry>;
};

