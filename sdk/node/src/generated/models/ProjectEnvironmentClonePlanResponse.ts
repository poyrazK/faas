/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectEnvironmentClonePlanActionResponse } from './ProjectEnvironmentClonePlanActionResponse.js';
/**
 * Read-only, non-secret preflight of one environment clone.
 */
export type ProjectEnvironmentClonePlanResponse = {
  project_slug: string;
  from_environment: string;
  to_environment: string;
  share_resources: boolean;
  /**
   * False when the target exists or a managed-resource prerequisite is not met. Quotas are checked again during creation.
   */
  can_clone: boolean;
  /**
   * True when every source workload has a live release available for subsequent promotion.
   */
  can_promote: boolean;
  workload_count: number;
  actions: Array<ProjectEnvironmentClonePlanActionResponse>;
  blocking_reasons: Array<string>;
  warnings: Array<string>;
};

