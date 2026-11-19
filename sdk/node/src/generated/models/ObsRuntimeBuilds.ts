/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeAppTaskHealth } from './ObsRuntimeAppTaskHealth.js';
import type { ObsRuntimeAppTaskOperations } from './ObsRuntimeAppTaskOperations.js';
import type { ObsRuntimeAppTaskResults } from './ObsRuntimeAppTaskResults.js';
import type { ObsRuntimeBuildCounts } from './ObsRuntimeBuildCounts.js';
import type { ObsRuntimeBuilderHealth } from './ObsRuntimeBuilderHealth.js';
import type { ObsRuntimeBuildItem } from './ObsRuntimeBuildItem.js';
import type { ObsRuntimeBuildJob } from './ObsRuntimeBuildJob.js';
import type { ObsRuntimeBuildRecoveryHealth } from './ObsRuntimeBuildRecoveryHealth.js';
import type { ObsRuntimeCommandCronHealth } from './ObsRuntimeCommandCronHealth.js';
import type { ObsRuntimeCommandCronOutcomes } from './ObsRuntimeCommandCronOutcomes.js';
import type { ObsRuntimeCommandCronTiming } from './ObsRuntimeCommandCronTiming.js';
import type { ObsRuntimeDelayedTaskDispatch } from './ObsRuntimeDelayedTaskDispatch.js';
import type { ObsRuntimeDiagnostics } from './ObsRuntimeDiagnostics.js';
import type { ObsRuntimeHandoffHealth } from './ObsRuntimeHandoffHealth.js';
import type { ObsRuntimeHTTPCronDelivery } from './ObsRuntimeHTTPCronDelivery.js';
import type { ObsRuntimeLoopHealth } from './ObsRuntimeLoopHealth.js';
import type { ObsRuntimePostCaptureHealth } from './ObsRuntimePostCaptureHealth.js';
import type { ObsRuntimePreparationHealth } from './ObsRuntimePreparationHealth.js';
import type { ObsRuntimeProcess } from './ObsRuntimeProcess.js';
import type { ObsRuntimeReadiness } from './ObsRuntimeReadiness.js';
import type { ObsRuntimeSnapshotPrimeHealth } from './ObsRuntimeSnapshotPrimeHealth.js';
/**
 * Advisory sampled identity evidence; full-set counts and a bounded item preview.
 */
export type ObsRuntimeBuilds = {
  generated_at: string;
  scope: 'selected_critical_daemon_builds';
  expected_role: '' | 'single-box-v1' | 'control-plane-v1' | 'single-box-v2' | 'control-plane-v2';
  requested_release: string;
  evidence_after: string;
  state: 'unknown' | 'incomplete' | 'matching' | 'mismatched' | 'mixed';
  reason?: string;
  max_age_seconds: 120;
  counts: ObsRuntimeBuildCounts;
  readiness?: ObsRuntimeReadiness;
  process?: ObsRuntimeProcess;
  loop_health?: ObsRuntimeLoopHealth;
  builder_health?: ObsRuntimeBuilderHealth;
  handoff_health?: ObsRuntimeHandoffHealth;
  preparation_health?: ObsRuntimePreparationHealth;
  snapshot_prime_health?: ObsRuntimeSnapshotPrimeHealth;
  command_cron_outcomes?: ObsRuntimeCommandCronOutcomes;
  command_cron_timing?: ObsRuntimeCommandCronTiming;
  diagnostics?: ObsRuntimeDiagnostics;
  http_cron_delivery?: ObsRuntimeHTTPCronDelivery;
  delayed_task_dispatch?: ObsRuntimeDelayedTaskDispatch;
  app_task_results?: ObsRuntimeAppTaskResults;
  app_task_operations?: ObsRuntimeAppTaskOperations;
  app_task_health?: ObsRuntimeAppTaskHealth;
  command_cron_health?: ObsRuntimeCommandCronHealth;
  post_capture_health?: ObsRuntimePostCaptureHealth;
  build_recovery_health?: ObsRuntimeBuildRecoveryHealth;
  jobs: Array<ObsRuntimeBuildJob>;
  items: Array<ObsRuntimeBuildItem>;
  omitted_items: number;
  limitations: Array<string>;
};

