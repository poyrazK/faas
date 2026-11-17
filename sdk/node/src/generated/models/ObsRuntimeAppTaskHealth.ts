/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeAppTaskClassCounts } from './ObsRuntimeAppTaskClassCounts.js';
import type { ObsRuntimeAppTaskCounts } from './ObsRuntimeAppTaskCounts.js';
import type { ObsRuntimeAppTaskItem } from './ObsRuntimeAppTaskItem.js';
/**
 * Advisory release, manual and cron task evidence for v2 scheduler jobs. Each scheduler observes the shared fleet independently. Compare views without summing task counts. Local worker slots do not measure fleet tasks or VMs. No deployment health or eligibility verdict is inferred.
 */
export type ObsRuntimeAppTaskHealth = {
  scope: 'replica_observations_of_shared_fleet';
  state: 'nonempty' | 'empty' | 'incomplete' | 'unknown';
  reason?: 'app_task_evidence_observed' | 'incomplete_app_task_evidence' | 'app_task_evidence_unavailable';
  counts: ObsRuntimeAppTaskCounts;
  classes: Array<ObsRuntimeAppTaskClassCounts>;
  incomplete: boolean;
  items: Array<ObsRuntimeAppTaskItem>;
  omitted_items: number;
};

