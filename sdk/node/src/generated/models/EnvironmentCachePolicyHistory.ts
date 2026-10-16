/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EnvironmentCachePolicy } from './EnvironmentCachePolicy.js';
/**
 * Bounded retained cache policy snapshots for one immutable named environment, ordered by descending revision.
 */
export type EnvironmentCachePolicyHistory = {
  app_id: string;
  project_id: string;
  environment_id: string;
  scope: string;
  current_revision: number;
  entries: Array<EnvironmentCachePolicy>;
  /**
   * Exclusive cursor for the next older page; omitted at the end.
   */
  next_before_revision?: number;
};

