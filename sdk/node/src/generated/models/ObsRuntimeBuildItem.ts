/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeProcessItem } from './ObsRuntimeProcessItem.js';
import type { ObsRuntimeReadinessItem } from './ObsRuntimeReadinessItem.js';
/**
 * Sanitized target observation or missing expectation; a SHA is present only on fresh unambiguous build evidence.
 */
export type ObsRuntimeBuildItem = {
  job: string;
  daemon: string;
  node?: string;
  target_id?: string;
  state: 'matching' | 'mismatched' | 'missing' | 'stale' | 'unavailable';
  reason?: string;
  unexpected?: boolean;
  git_sha?: string;
  version?: string;
  observed_at?: string;
  scrape_observed_at?: string;
  readiness?: ObsRuntimeReadinessItem;
  process?: ObsRuntimeProcessItem;
};

