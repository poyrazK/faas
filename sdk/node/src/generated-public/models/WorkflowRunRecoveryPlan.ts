/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowRunRecoveryTarget } from './WorkflowRunRecoveryTarget.js';
/**
 * Explicit reviewed native workflow runs. Requires workflow mode and request_id; excludes event selections and receipt protection. Each resume checks the original count and eligibility atomically with its job outcome. Concurrency pressure leaves the item pending; changed or unsafe targets are skipped. Queued means a resume was admitted, not execution success. No new runs are selected. The original normalized request returns the retained job on retry; changes conflict.
 */
export type WorkflowRunRecoveryPlan = {
  /**
   * Advisory deadline from each admission. Zero disables it; nonzero values must be at least 60 seconds.
   */
  execution_deadline_seconds?: number;
  /**
   * No observable progress threshold. Zero uses 900 seconds; nonzero values must be at least 60 seconds. Scheduled and capacity waits do not trigger stalled alerts.
   */
  execution_stall_seconds?: number;
  name: string;
  runs: Array<WorkflowRunRecoveryTarget>;
};

