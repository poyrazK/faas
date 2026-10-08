/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Account service map for `GET /v1/service-map` (ADR-732). `nodes` are
 * the apps on at least one returned edge, sorted by slug; `edges` are
 * sorted by `calls` descending. Degraded responses carry null `nodes`
 * and `edges`.
 *
 */
export type ServiceMapResponse = {
  range: string;
  /**
   * "prometheus" or "degraded: <reason>".
   */
  source: string;
  as_of: string;
  truncated: boolean;
  nodes: any[] | null;
  edges: any[] | null;
};

