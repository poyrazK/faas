/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EnvironmentCachePolicySource } from './EnvironmentCachePolicySource.js';
import type { EnvironmentCacheRule } from './EnvironmentCacheRule.js';
/**
 * Complete named-environment replacement with immutable identity, revision guard and transactional retry ID.
 */
export type SetEnvironmentCachePolicyRequest = {
  app_id: string;
  project_id: string;
  environment_id: string;
  /**
   * Stable native operation ID; keep this ID and body on retries.
   */
  request_id: string;
  expected_revision: number;
  /**
   * Optional; true restores live app cache inheritance at a new revision and requires rules=[]. Omitted or false replaces the override.
   */
  inherited?: boolean;
  copy_from?: EnvironmentCachePolicySource;
  /**
   * Optional historical revision to restore as a new revision. Rules and inherited mode must match retained history; zero restores live inheritance. Cannot combine with copy_from.
   */
  restore_revision?: number;
  /**
   * Complete replacement; [] disables environment caching unless inherited=true restores app rules. Omission or null is invalid.
   */
  rules: Array<EnvironmentCacheRule>;
};

