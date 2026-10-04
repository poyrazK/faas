/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { GlobalRuntimeQuotaShare } from './GlobalRuntimeQuotaShare.js';
/**
 * Immutable regional share allocation and optional placement of rollout headroom.
 */
export type PutGlobalRuntimeQuotaRequest = {
  /**
   * One positive share for every selected verified region; duplicate regions are rejected.
   */
  shares: Array<GlobalRuntimeQuotaShare>;
  /**
   * Selected region receiving rollout overlap; omitted or empty defaults to the first sorted region.
   */
  rollout_region?: string;
};

