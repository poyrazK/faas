/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * POST body for /v1/apps/{slug}/debug/compare.
 */
export type DebugCompareRequest = {
  /**
   * Source deployment id.
   */
  source: string;
  /**
   * Mirror deployment id.
   */
  mirror: string;
  /**
   * Optional exact-match route filter.
   */
  route?: string | null;
  /**
   * Lookback duration (e.g. '24h').
   */
  since?: string | null;
  /**
   * Window end (RFC3339). Empty = now.
   */
  until?: string | null;
};

