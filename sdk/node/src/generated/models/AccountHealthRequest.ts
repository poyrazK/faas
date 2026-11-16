/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One refused request: the matched route pattern, the problem code and the ids to quote to support.
 */
export type AccountHealthRequest = {
  /**
   * When the request was refused.
   */
  occurred_at: string;
  /**
   * Matched route pattern, for example POST /v1/apps/{slug}/deployments; never the raw URL.
   */
  route: string;
  /**
   * Problem code returned.
   */
  code: string;
  /**
   * App slug from the request path, when the route has one.
   */
  app_slug?: string;
  /**
   * X-Faas-Request-Id of the request.
   */
  request_id?: string;
  /**
   * X-Gregale-Trace-Id of the request.
   */
  trace_id?: string;
};

