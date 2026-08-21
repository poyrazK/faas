/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PlanAffectedApp } from './PlanAffectedApp.js';
import type { PlanCron } from './PlanCron.js';
import type { PlanManaged } from './PlanManaged.js';
import type { PlanWorkload } from './PlanWorkload.js';
/**
 * Dry-run scan response.
 */
export type PlanResponse = {
  project_slug: string;
  repo_full_name?: string;
  scan_source: 'compose' | 'procfile' | 'k8s' | 'render' | 'fly' | 'serverless' | 'workspace' | 'convention' | 'single' | 'unknown';
  tier: string;
  workloads: Array<PlanWorkload>;
  managed: Array<PlanManaged>;
  crons: Array<PlanCron>;
  warnings?: Array<string>;
  observed_apps: number;
  observed_crons: number;
  limit_apps: number;
  limit_crons: number;
  can_apply: boolean;
  crons_not_allowed?: boolean;
  /**
   * base64-JSON plan token; pass back as ?plan_token= on /v1/projects to skip the second extract.
   */
  plan_token: string;
  will_deploy?: Array<PlanAffectedApp>;
  unaffected?: Array<PlanAffectedApp>;
  skipped?: Array<PlanAffectedApp>;
  removed?: Array<string>;
};

