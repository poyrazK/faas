/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EnvironmentCacheRule } from './EnvironmentCacheRule.js';
/**
 * Desired cache mode and monotonic revision for one named environment; absence preserves app inheritance at revision zero.
 */
export type EnvironmentCachePolicy = {
  app_id: string;
  project_id: string;
  environment_id: string;
  scope: string;
  revision: number;
  /**
   * True at revision zero or a positive restored-inheritance revision; inherited app rule bodies are not returned.
   */
  inherited: boolean;
  rules: Array<EnvironmentCacheRule>;
  updated_at?: string;
};

