/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeAppTaskKindName } from './ObsRuntimeAppTaskKindName.js';
import type { ObsRuntimeAppTaskResultName } from './ObsRuntimeAppTaskResultName.js';
import type { ObsRuntimeAppTaskResultSource } from './ObsRuntimeAppTaskResultSource.js';
/**
 * Full-set coverage for one fixed source/kind/outcome. Ordered completion kinds are release, manual, cron, each with succeeded, failed, timed_out, cancelled, queued, followed by recovery requeued_restores, failed_runs, cancelled. Kind is required for completion and absent for recovery. Counts describe signals, not numbers of tasks or acknowledged calls.
 */
export type ObsRuntimeAppTaskResultOutcome = {
  source: ObsRuntimeAppTaskResultSource;
  kind?: ObsRuntimeAppTaskKindName;
  outcome: ObsRuntimeAppTaskResultName;
  increased: number;
  unchanged: number;
  baseline: number;
  unknown: number;
};

