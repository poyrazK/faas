/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppliedBuild } from './AppliedBuild.js';
import type { PlanAffectedApp } from './PlanAffectedApp.js';
import type { PlanAsyncRoute } from './PlanAsyncRoute.js';
import type { PlanCron } from './PlanCron.js';
import type { PlanDetectionWarning } from './PlanDetectionWarning.js';
import type { PlanManaged } from './PlanManaged.js';
import type { PlanWorkload } from './PlanWorkload.js';
import type { ProjectSafeRelease } from './ProjectSafeRelease.js';
/**
 * Apply response. Carries the inserted project_id and per-app IDs.
 */
export type ApplyResponse = {
  safe_release?: ProjectSafeRelease;
  submission_attempt_id?: string;
  project_slug: string;
  repo_full_name?: string;
  scan_source: string;
  tier?: string;
  workloads?: Array<PlanWorkload>;
  managed?: Array<PlanManaged>;
  crons?: Array<PlanCron>;
  /**
   * Structured async-route reconciliation returned with the applied plan.
   */
  async_routes?: Array<PlanAsyncRoute>;
  warnings?: Array<string>;
  /**
   * Structured skipped/merged detector decisions returned with the applied plan.
   */
  detection_warnings?: Array<PlanDetectionWarning>;
  observed_apps?: number;
  observed_crons?: number;
  limit_apps?: number;
  limit_crons?: number;
  can_apply: boolean;
  crons_not_allowed?: boolean;
  plan_token: string;
  can_apply_pre_exclude?: boolean;
  gate_rescued_by_exclude?: boolean;
  can_apply_reasons?: Array<string>;
  will_deploy?: Array<PlanAffectedApp>;
  unaffected?: Array<PlanAffectedApp>;
  skipped?: Array<PlanAffectedApp>;
  removed?: Array<string>;
  persisted_exclusions?: Array<string>;
  stale_persisted_exclusions?: Array<string>;
  project_id?: string;
  apps?: Array<{
    slug: string;
    id: string;
  }>;
  /**
   * Per-workload build enqueue results. Populated when the apply
   * path actually enqueued one (deployment, build) per added or
   * changed workload (repo decomposition close-
   * the-loop). On staging or enqueue failure the per-app Error
   * field is populated and the IDs are empty.
   *
   */
  builds?: Array<AppliedBuild>;
  /**
   * Slugs in apps that this apply did not rebuild: nothing in them
   * changed, or --only left them out. apps is the whole project
   * membership, so a client compares it with builds plus unchanged.
   *
   */
  unchanged?: Array<string>;
};

