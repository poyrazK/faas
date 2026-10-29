/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Limit + observed extension on a plan-quota problem. Mirrors
 * api.Problem.WithLimit; emitted alongside any 402/403 quota
 * response so the CLI can render "X/Y apps" without a second
 * request.
 *
 */
export type QuotaBlock = {
  limit?: number;
  observed?: number;
  docs_url?: string;
};

