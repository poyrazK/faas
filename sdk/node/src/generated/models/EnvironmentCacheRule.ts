/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EdgeRuleCacheAction } from './EdgeRuleCacheAction.js';
/**
 * An unconditional cache rule owned by exactly one immutable named environment host.
 */
export type EnvironmentCacheRule = {
  /**
   * Concrete cache path glob; OpenAPI templates and backslashes are rejected.
   */
  path: string;
  /**
   * Must be distinct within this replacement.
   */
  priority: number;
  cache: EdgeRuleCacheAction;
};

