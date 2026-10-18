/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One ordered fallback step; model replaces the request's "model" for this target.
 */
export type OutboundAIRouteTarget = {
  integration_id: string;
  model?: string;
  /**
   * A weighted route's share of first attempts for this target; 0 makes it a fallback only (ADR-1434).
   */
  weight?: number;
};

