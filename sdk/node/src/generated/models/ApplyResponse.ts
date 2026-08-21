/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppliedBuild } from './AppliedBuild.js';
import type { PlanAffectedApp } from './PlanAffectedApp.js';
import type { PlanCron } from './PlanCron.js';
import type { PlanManaged } from './PlanManaged.js';
import type { PlanWorkload } from './PlanWorkload.js';
/**
 * Apply response. Carries the inserted project_id and per-app IDs.
 */
export type ApplyResponse = {
  project_slug: string;
  repo_full_name?: string;
  scan_source: string;
  tier?: string;
  workloads?: Array<PlanWorkload>;
  managed?: Array<PlanManaged>;
  crons?: Array<PlanCron>;
  warnings?: Array<string>;
  observed_apps?: number;
  observed_crons?: number;
  limit_apps?: number;
  limit_crons?: number;
  can_apply: boolean;
  crons_not_allowed?: boolean;
  plan_token: string;
  will_deploy?: Array<PlanAffectedApp>;
  unaffected?: Array<PlanAffectedApp>;
  skipped?: Array<PlanAffectedApp>;
  removed?: Array<string>;
  project_id?: string;
  apps?: Array<{
    slug: string;
    id: string;
  }>;
  /**
   * Per-workload build enqueue results. Populated when the apply
   * path actually enqueued one (deployment, build) per added or
   * changed workload (PR-A, repo decomposition Phase 5 close-
   * the-loop). On staging or enqueue failure the per-app Error
   * field is populated and the IDs are empty.
   *
   */
  builds?: Array<AppliedBuild>;
};

